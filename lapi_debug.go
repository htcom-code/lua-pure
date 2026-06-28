package luapure

import "errors"

// Go-native debugging surface for embedders building a debugger (a DAP server,
// a CLI console, a tracer). PUC exposes this through the Lua `debug` library and
// the C lua_Hook / lua_getstack / lua_getlocal calls; here the same primitives
// are reachable directly from Go, so a host need not round-trip through Lua to
// install a hook, walk the stack, or read a frame's locals.
//
// Two layers live here:
//   - SetGoHook installs a Go callback fired by the VM at call/return/line/count
//     events (the lua_sethook analogue).
//   - Frame is a transient handle to a live call frame, valid only while the VM
//     is paused inside a hook; it reads source position, locals, upvalues and
//     varargs (the lua_getstack + lua_getlocal/lua_getupvalue analogues).
//
// The higher-level breakpoint/step machine built on top is the Debugger
// (debug_session.go).

// HookEvent identifies why a GoHook fired, mirroring PUC's LUA_HOOK* events.
type HookEvent uint8

const (
	HookCall     HookEvent = iota // entering a Lua function
	HookReturn                    // a Lua function is about to return
	HookLine                      // execution reached a new source line
	HookCount                     // the instruction-count quantum elapsed
	HookTailCall                  // entering a function by a tail call
)

// String renders the event the way the Lua hook's first argument spells it.
func (e HookEvent) String() string {
	switch e {
	case HookCall:
		return "call"
	case HookReturn:
		return "return"
	case HookLine:
		return "line"
	case HookCount:
		return "count"
	case HookTailCall:
		return "tail call"
	}
	return "?"
}

// HookMask selects which events fire a GoHook; OR the constants together. It
// mirrors PUC's LUA_MASK* bits.
type HookMask int

const (
	MaskCall   HookMask = maskCall
	MaskReturn HookMask = maskRet
	MaskLine   HookMask = maskLine
	MaskCount  HookMask = maskCount
)

// GoHook is a debug callback. It runs inline on the goroutine executing the VM,
// with no extra Lua frame pushed, so it can inspect the current frame (via
// L.Frame) directly. For a line event, line is the source line; otherwise it is
// -1. The hook may call back into the VM (e.g. L.Call to evaluate an
// expression) — nested events do not re-enter the hook while it runs.
type GoHook func(L *LState, event HookEvent, line int)

// SetGoHook installs h as the Go debug hook, firing on the events named by mask;
// when mask includes MaskCount, count is the instruction quantum between count
// events. A nil h (or zero mask) removes the hook. It is independent of any Lua
// debug.sethook hook, which keeps firing alongside it.
func (L *LState) SetGoHook(h GoHook, mask HookMask, count int) {
	if h == nil {
		L.goHook = nil
		L.goHookMask = 0
		L.goHookCount = 0
		L.goHookCdown = 0
		return
	}
	L.goHook = h
	L.goHookMask = int(mask)
	if mask&MaskCount != 0 && count > 0 {
		L.goHookCount = count
		L.goHookCdown = count
	} else {
		L.goHookMask &^= maskCount
		L.goHookCount = 0
		L.goHookCdown = 0
	}
	L.allowHook = true
	// As PUC does when a line hook is installed mid-run: sync the caller frame's
	// last-seen line so the line the hook was set on does not immediately re-fire.
	if L.goHookMask&maskLine != 0 && L.ci != nil {
		if caller := L.ci.prev; caller != nil && caller.isLuaFrame() {
			if cl := L.stack[caller.fn].closure(); cl != nil && cl.isLua() {
				caller.lastLine = cl.proto.LineAt(caller.savedpc - 1)
				caller.lastHkPc = caller.savedpc - 1
			}
		}
	}
}

// ClearGoHook removes the Go debug hook.
func (L *LState) ClearGoHook() { L.SetGoHook(nil, 0, 0) }

// fireGoHook invokes the Go hook re-entrancy-safe: allowHook is false for the
// duration so nested events (and the hook calling back into the VM) fire nothing.
func (L *LState) fireGoHook(ev HookEvent, line int) {
	if L.goHook == nil || !L.allowHook {
		return
	}
	L.allowHook = false
	defer func() { L.allowHook = true }()
	L.goHook(L, ev, line)
}

// --- call-frame inspection (lua_getstack / lua_getlocal / lua_getupvalue) ---

// Frame is a handle to a live call frame. It is valid only transiently — while
// the VM is paused inside a hook the frame belongs to — because the underlying
// call-info and stack slots are reused once execution resumes. Do not retain a
// Frame past the hook that produced it.
type Frame struct {
	L     *LState
	ci    *callInfo
	level int
}

// Local pairs a frame-local variable's name with its current value (Frame.Locals).
type Local struct {
	Name  string // a source name, or "(temporary)" / "(vararg)" for unnamed slots
	Index int    // 1-based index for Frame.Local / Frame.SetLocal
	Value Value
}

// StackDepth reports how many active call frames there are (the current frame
// plus its callers). Frame levels run 0 .. StackDepth()-1.
func (L *LState) StackDepth() int {
	n := 0
	for ci := L.ci; ci != nil; ci = ci.prev {
		n++
	}
	return n
}

// Frame returns the call frame at the given level: 0 is the currently executing
// frame, 1 its caller, and so on. ok is false when level is out of range.
func (L *LState) Frame(level int) (f Frame, ok bool) {
	ci := L.ciAtLevel(level)
	if ci == nil {
		return Frame{}, false
	}
	return Frame{L: L, ci: ci, level: level}, true
}

// Level is the frame's stack level (0 = innermost), as passed to LState.Frame.
func (f Frame) Level() int { return f.level }

func (f Frame) closure() *Closure {
	if f.ci == nil || f.ci.fn < 0 || f.ci.fn >= len(f.L.stack) {
		return nil
	}
	return f.L.stack[f.ci.fn].closure()
}

// IsLua reports whether the frame runs a Lua function (false for a native one).
func (f Frame) IsLua() bool { return f.ci.isLuaFrame() }

// IsTailCall reports whether the frame was entered by a tail call (so its caller
// frame was replaced).
func (f Frame) IsTailCall() bool { return f.ci.status&cistTail != 0 }

// Func returns the function value running in the frame.
func (f Frame) Func() Value {
	if f.ci == nil || f.ci.fn < 0 || f.ci.fn >= len(f.L.stack) {
		return Nil
	}
	return f.L.stack[f.ci.fn]
}

// Source returns the frame function's chunk name in PUC's raw form ("@file",
// "=name", or the source string); "=[C]" for a native frame.
func (f Frame) Source() string {
	if cl := f.closure(); cl != nil && cl.isLua() {
		return cl.proto.Source
	}
	return "=[C]"
}

// ShortSource returns the chunk name formatted for display (shortSrc), matching
// the short_src field of debug.getinfo.
func (f Frame) ShortSource() string { return shortSrc(f.Source()) }

// CurrentLine returns the source line the frame is stopped on, or -1 for a
// native frame or one with stripped line info.
func (f Frame) CurrentLine() int {
	if cl := f.closure(); cl != nil && cl.isLua() {
		return cl.proto.LineAt(f.ci.savedpc - 1)
	}
	return -1
}

// FuncName returns the frame function's name and namewhat as debug.getinfo's "n"
// option computes them from the call site (e.g. "f", "method"); both are empty
// when no name can be inferred.
func (f Frame) FuncName() (name, what string) {
	what, name = f.L.funcNameForCI(f.ci)
	return name, what
}

// NumParams returns the number of fixed parameters of a Lua frame's function
// (0 for a native frame).
func (f Frame) NumParams() int {
	if cl := f.closure(); cl != nil && cl.isLua() {
		return int(cl.proto.NumParams)
	}
	return 0
}

// IsVararg reports whether a Lua frame's function takes varargs.
func (f Frame) IsVararg() bool {
	if cl := f.closure(); cl != nil && cl.isLua() {
		return cl.proto.IsVararg
	}
	return false
}

// Local returns the frame's n-th local variable (1-based), the lua_getlocal
// form: its source name (or "(temporary)"/"(vararg)") and current value. ok is
// false when n is out of range.
func (f Frame) Local(n int) (name string, v Value, ok bool) {
	nm, slot, ok := f.L.findLocal(f.ci, n)
	if !ok {
		return "", Nil, false
	}
	return nm, f.L.stack[slot], true
}

// SetLocal assigns the frame's n-th local variable (1-based), the lua_setlocal
// form. It reports false when n is out of range.
func (f Frame) SetLocal(n int, v Value) bool {
	_, slot, ok := f.L.findLocal(f.ci, n)
	if !ok {
		return false
	}
	f.L.stack[slot] = v
	return true
}

// Locals enumerates the frame's active local variables in declaration order
// (the indices Local/SetLocal accept). Unnamed in-range slots appear as
// "(temporary)"; callers wanting only source-named locals can skip names that
// begin with "(".
func (f Frame) Locals() []Local {
	var out []Local
	for n := 1; ; n++ {
		nm, slot, ok := f.L.findLocal(f.ci, n)
		if !ok {
			break
		}
		out = append(out, Local{Name: nm, Index: n, Value: f.L.stack[slot]})
	}
	return out
}

// Vararg returns the frame's n-th extra (vararg) argument (1-based), or ok=false
// when the frame is not vararg or n is out of range.
func (f Frame) Vararg(n int) (v Value, ok bool) {
	cl := f.closure()
	if cl == nil || !cl.isLua() {
		return Nil, false
	}
	slot, ok := varargSlot(f.L, f.ci, cl, n)
	if !ok {
		return Nil, false
	}
	return f.L.stack[slot], true
}

// Eval compiles and runs a Lua expression in the scope of the frame, the way a
// debugger console's "print" or a watch expression does: a bare name resolves
// to the frame's locals first, then its upvalues, then globals, and an
// assignment writes back to whichever it found (or to a new global). The
// expression's results are returned; a statement (e.g. "x = 1") is accepted too.
//
// Eval is for use while the program is paused at the frame (from a Debugger
// OnStop handler). The debug hook is suppressed for the duration, so evaluating
// does not itself trip breakpoints or recurse into the handler.
func (f Frame) Eval(expr string) ([]Value, error) {
	saved := f.L.allowHook
	f.L.allowHook = false
	defer func() { f.L.allowHook = saved }()

	env := f.scopeEnv()
	// Prefer expression form so the value comes back; fall back to a statement
	// chunk only when "return <expr>" does not compile.
	res, err := f.L.RunWith(env, "return "+expr, "=(eval)")
	if err == nil {
		return res, nil
	}
	var ce *CompileError
	if !errors.As(err, &ce) {
		return nil, err // a runtime error from the expression: report it as-is
	}
	return f.L.RunWith(env, expr, "=(eval)")
}

// scopeEnv builds a proxy _ENV table whose __index/__newindex resolve names
// against the frame's locals, then upvalues, then the real globals — so an
// evaluated chunk sees the frame's scope.
func (f Frame) scopeEnv() *Table {
	locals := map[string]int{}
	for _, lv := range f.Locals() {
		if lv.Name != "" && lv.Name[0] != '(' { // skip "(temporary)"/"(vararg)"
			locals[lv.Name] = lv.Index // a later (inner) declaration shadows
		}
	}
	ups := map[string]int{}
	for n := 1; ; n++ {
		nm, _, ok := f.Upvalue(n)
		if !ok {
			break
		}
		if nm != "" && nm != "(no name)" && nm != "_ENV" {
			ups[nm] = n
		}
	}
	gv := mkTable(f.L.globals)
	proxy := newTable()
	mt := newTable()
	mt.rawset(MkString("__index"), NewGoFunc("__index", func(L *LState) int {
		key := L.Arg(2)
		if key.IsString() {
			name := key.Str()
			if idx, ok := locals[name]; ok {
				_, v, _ := f.Local(idx)
				L.Push(v)
				return 1
			}
			if un, ok := ups[name]; ok {
				_, v, _ := f.Upvalue(un)
				L.Push(v)
				return 1
			}
		}
		L.Push(L.indexGet(gv, key)) // fall back to globals (honouring their metatable)
		return 1
	}))
	mt.rawset(MkString("__newindex"), NewGoFunc("__newindex", func(L *LState) int {
		key := L.Arg(2)
		val := L.Arg(3)
		if key.IsString() {
			name := key.Str()
			if idx, ok := locals[name]; ok {
				f.SetLocal(idx, val)
				return 0
			}
			if un, ok := ups[name]; ok {
				f.SetUpvalue(un, val)
				return 0
			}
		}
		L.settable(gv, key, val)
		return 0
	}))
	proxy.meta = mt
	return proxy
}

// NumUpvalues returns the count of upvalues of the frame's function.
func (f Frame) NumUpvalues() int {
	cl := f.closure()
	if cl == nil {
		return 0
	}
	if cl.isLua() {
		return len(cl.upvals)
	}
	return len(cl.goUpvals)
}

// Upvalue returns the frame function's n-th upvalue (1-based): its name (empty
// for a native closure's upvalues, "(no name)" for a stripped Lua chunk) and
// value. ok is false when n is out of range.
func (f Frame) Upvalue(n int) (name string, v Value, ok bool) {
	cl := f.closure()
	if cl == nil {
		return "", Nil, false
	}
	return upvalAt(cl, n)
}

// SetUpvalue assigns the frame function's n-th upvalue (1-based). It reports
// false when n is out of range.
func (f Frame) SetUpvalue(n int, v Value) bool {
	cl := f.closure()
	if cl == nil {
		return false
	}
	if cl.isLua() {
		if n < 1 || n > len(cl.upvals) {
			return false
		}
		cl.upvals[n-1].set(v)
		return true
	}
	if n < 1 || n > len(cl.goUpvals) {
		return false
	}
	cl.goUpvals[n-1] = v
	return true
}
