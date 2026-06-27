package luapure

import (
	"fmt"
	"strings"
	"sync"
)

// LState is a Lua 5.4 execution state (one thread). It owns the value stack,
// the chain of CallInfo frames, the open-upvalue and to-be-closed lists, and a
// reference to the shared globals. Coroutines (multiple threads sharing a
// global_State) are out of scope for this first VM; LState fuses the per-thread
// and global state into one object.
type LState struct {
	stack  []Value    // value stack; registers are absolute indices
	top    int        // first free slot (PUC L->top)
	ci     *callInfo  // current call frame
	openuv []*Upvalue // open upvalues, ordered by descending level
	tbc    []int      // stack indices of pending to-be-closed variables (ascending)

	globals   *Table              // the globals table (_ENV)
	registry  *Table              // the registry (LUA_REGISTRYINDEX analogue)
	basicMT   [numTypeTags]*Table // metatables for the basic types (G->mt)
	stringMT  *Table              // shared metatable for strings
	pkgLoaded *Table              // package.loaded (set by the package library)
	pkgTable  *Table              // the package table itself; require captures this
	//                              like PUC's upvalue, so a reassigned global
	//                              'package' does not break module loading.

	// debug hook state (set by debug.sethook).
	hook            Value // hook function (nil if none)
	hookMask        int   // LUA_MASK* bits
	hookCount       int   // reset value for the count hook (0 = off)
	hookCdown       int   // instructions remaining until the next count hook
	allowHook       bool  // false while inside a hook (no re-entry)
	pendingHookMark bool  // tag the next frame created as the hook frame (CIST_HOOK)
	pendingFinMark  bool  // tag the next frame created as a __gc finalizer (CIST_FIN)

	nCcalls    int // nested Go-level call depth (LUAI_MAXCCALLS guard)
	errReg     int // register of the value a failing opcode operated on (-1 = none)
	errUpval   int // upvalue index a failing GETTABUP operated on (-1 = none)
	finGCTick  int // instructions since the last finalizer poll (finGCPoll)
	weakGCTick int // finalizer polls since the last weak-table GC nudge

	// coroutine state (per thread). Each coroutine runs in its own goroutine;
	// resume/yield hand off cooperatively over these channels so only one is
	// ever active. Threads share the global tables by pointer.
	co          *coState
	status      int
	started     bool
	resumeCh    chan []Value
	yieldCh     chan coMsg
	deathErr    *LuaError // error that killed this coroutine (reported by close)
	coYieldVals []Value   // values carried by a stackless yield panic to resumeSync

	// Stackless coroutine driving. A coroutine first runs synchronously on the
	// resumer's own goroutine (coSyncActive), with yield unwinding via panic. If
	// it reaches a Go-recursion boundary that may yield (a metamethod or pcall),
	// it cannot suspend synchronously, so it promotes: a goroutine takes over and
	// the rest of its life uses the channel handoff (promoted). Pure-Lua
	// coroutines never promote and pay no goroutine cost.
	coSyncActive bool
	promoted     bool

	// math.random PRNG state (xoshiro256**). Shared by pointer across the VM's
	// threads, matching PUC's single RanState upvalue bound at library open.
	rng *rngState

	// message handler for the innermost xpcall (PUC L->errfunc): invoked at the
	// point an error is signalled, while the stack is still live, so a handler
	// like debug.traceback sees the erroring frames. inErrfunc guards against
	// re-entry when the handler itself errors.
	errfunc   Value
	inErrfunc bool

	// noYield > 0 while a library callback that PUC invokes non-yieldably is on
	// the stack (a sort comparator, a gsub replacement function). Our coroutines
	// are goroutines and could yield through such a Go frame, but PUC forbids it,
	// so coroutine.yield errors when this is set (a C-call boundary).
	noYield int
}

// callNoYield calls fn the way PUC's luaD_callnoyield does — across a C-call
// boundary that a coroutine may not yield through.
func (L *LState) callNoYield(fn Value, args []Value, nret int) []Value {
	L.noYield++
	defer func() { L.noYield-- }()
	return L.CallValue(fn, args, nret)
}

// coState is the per-VM coroutine coordination shared by all its threads.
type coState struct {
	mainThread *LState

	// Finalizer queue (see finalizer.go). A Go finalizer fires on a separate
	// goroutine and only enqueues the object here under finMu; the Lua main
	// thread drains the queue and runs __gc synchronously. finPending mirrors
	// len(finQueue) so the VM loop can poll without taking the lock.
	finMu      sync.Mutex
	finQueue   []Value
	finPending int32 // queued, not yet drained
}

// Coroutine statuses (lua.h LUA_YIELD etc. / coroutine.status strings).
const (
	coRunning = iota
	coSuspended
	coNormal
	coDead
)

// coMsg is the value handed from a coroutine to its resumer.
type coMsg struct {
	kind int // coYield / coReturn / coError
	vals []Value
	err  *LuaError
}

const (
	coYieldMsg = iota
	coReturnMsg
	coErrorMsg
)

// Hook event masks (lua.h LUA_MASK*).
const (
	maskCall  = 1 << 0
	maskRet   = 1 << 1
	maskLine  = 1 << 2
	maskCount = 1 << 3
)

const numTypeTags = 9 // TypeNil..TypeThread (lua.h LUA_NUMTAGS region we use)

// Stack sizing constants (lstate.h).
const (
	extraStack  = 5
	luaMinStack = 20
	multRet     = -1 // LUA_MULTRET
)

// CallInfo status bits (lstate.h CIST_*), only those the VM consults.
const (
	cistC     = 1 << 1 // running a native function
	cistFresh = 1 << 2 // fresh execute() frame; return ends the loop
	cistTail  = 1 << 5 // tail-called frame
	cistHook  = 1 << 6 // frame is the debug hook function (CIST_HOOK)
	cistFin   = 1 << 7 // frame is a __gc finalizer (CIST_FIN)
)

// callInfo mirrors PUC struct CallInfo, with stack pointers replaced by indices.
type callInfo struct {
	fn       int // stack index of the function object
	base     int // first register = fn + 1 (Lua functions)
	top      int // top reserved for this frame
	savedpc  int // resume point: index into proto.Code
	nresults int // expected results (LUA_MULTRET for "all")
	status   uint16
	nextra   int // # extra (vararg) args for this frame
	nres     int // saved result count while closing tbc on return
	lastLine int // last source line a line hook fired on, for this frame
	lastHkPc int // pc the line hook last saw (PUC oldpc), for backward-jump detection

	// transfer info for a call/return hook (PUC ci->u2.transferinfo): the
	// 1-based local index of the first transferred value and how many, read by
	// debug.getinfo's "r" option.
	ftransfer int
	ntransfer int

	prev *callInfo
	// next links to a deeper frame kept for reuse after it returns (PUC
	// CallInfo.next): a call reuses prev.next instead of allocating, so steady
	// call traffic stops churning callInfo objects. Reset every other field on
	// reuse; never clear next.
	next *callInfo
}

// LuaError carries a Lua error value through Go's panic/recover (the longjmp
// replacement noted in the migration plan). It is the error type returned by
// the protected entry points (DoString, CallProto, Call), so embedders can
// type-assert to *LuaError to recover the raised Lua value and traceback.
type LuaError struct {
	value     Value
	traceback string
}

func (e *LuaError) Error() string {
	if e.value.IsString() {
		return e.value.Str()
	}
	if e.value.IsNumber() {
		return numToString(e.value)
	}
	return fmt.Sprintf("(error object is a %s value)", typeName(e.value))
}

// Value returns the raised Lua error object (commonly a string, but any value
// can be raised via error()).
func (e *LuaError) Value() Value { return e.value }

// Traceback returns the captured stack traceback, or "" if none was attached.
func (e *LuaError) Traceback() string { return e.traceback }

// NewState builds a fresh state with an empty globals table and registry.
func NewState() *LState {
	L := &LState{
		stack:     make([]Value, luaMinStack*2+extraStack),
		globals:   newTable(),
		registry:  newTable(),
		allowHook: true,
		errReg:    -1,
		errUpval:  -1,
		status:    coRunning,
		rng:       newRNG(),
	}
	L.co = &coState{mainThread: L}
	return L
}

// Globals returns the globals table (_ENV).
func (L *LState) Globals() *Table { return L.globals }

// --- stack helpers ---

// checkstack ensures at least n free slots above top (luaD_checkstack), growing
// the backing array if needed. Indices into the stack stay valid across growth.
// MaxStack (luaconf.go) bounds the value stack, so unbounded recursion raises
// "stack overflow" instead of exhausting memory.
func (L *LState) checkstack(n int) {
	// The hard limit applies to the logical slots requested (L.top + n), like
	// PUC luaD_growstack which compares the requested size to LUAI_MAXSTACK;
	// EXTRA_STACK is extra *allocation* beyond that, not counted against the
	// limit (otherwise the last extraStack slots of a max-size push — e.g.
	// table.unpack of ~LUAI_MAXSTACK values — would spuriously overflow).
	need := L.top + n
	// While a message handler / to-be-closed runs after a stack overflow
	// (inErrfunc), allow the ERRORSTACKSIZE reserve on top so the handler has
	// room (PUC luaD_growstack).
	if L.inErrfunc {
		// Overflowing even the reserve: PUC raises LUA_ERRERR.
		if need > MaxStack+ErrorStackReserve {
			L.runtimeError("error in error handling")
		}
	} else if need > MaxStack {
		L.runtimeError("stack overflow")
	}
	if alloc := need + extraStack; alloc > len(L.stack) {
		want := growSize(len(L.stack), alloc)
		if want > MaxStack+ErrorStackReserve {
			want = MaxStack + ErrorStackReserve
		}
		ns := make([]Value, want)
		copy(ns, L.stack)
		L.stack = ns
	}
}

func growSize(cur, need int) int {
	for cur < need {
		cur *= 2
	}
	return cur
}

func (L *LState) push(v Value) {
	L.checkstack(1)
	L.stack[L.top] = v
	L.top++
}

// --- error raising ---

// throw raises v as a Lua error.
func (L *LState) throw(v Value) {
	// PUC luaG_errormsg: if an xpcall message handler is active, run it here —
	// on the live stack, before unwinding — and propagate its result instead.
	if !L.errfunc.IsNil() && !L.inErrfunc {
		handler := L.errfunc
		L.inErrfunc = true
		hv := Nil
		errInHandler := false
		func() {
			defer func() {
				L.inErrfunc = false
				if r := recover(); r != nil {
					if _, ok := r.(*LuaError); ok {
						errInHandler = true // handler itself raised
					} else {
						panic(r)
					}
				}
			}()
			if res := L.CallValue(handler, []Value{v}, 1); len(res) > 0 {
				hv = res[0]
			}
		}()
		if errInHandler {
			// PUC LUA_ERRERR: an error inside the message handler yields the
			// fixed string "error in error handling".
			v = MkString("error in error handling")
		} else {
			v = hv
		}
	}
	panic(&LuaError{value: v})
}

// runtimeError raises a string error prefixed with the current source position,
// matching luaG_runerror's "chunkname:line: message" form.
func (L *LState) runtimeError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if L.ci != nil && L.ci.isLuaFrame() {
		cl := L.stack[L.ci.fn].closure()
		if cl != nil && cl.isLua() {
			line := cl.proto.LineAt(L.ci.savedpc - 1)
			src := shortSrc(cl.proto.Source)
			msg = fmt.Sprintf("%s:%d: %s", src, line, msg)
		}
	}
	L.throw(MkString(msg))
}

func (ci *callInfo) isLuaFrame() bool { return ci != nil && ci.status&cistC == 0 }

// shortSrc renders a chunk name like PUC's luaO_chunkid: "@file" -> "file",
// "=name" -> "name", otherwise a quoted/truncated form.
// shortSrc renders a chunk name for messages (luaO_chunkid): "=name" keeps the
// literal, "@file" the file name (truncated from the front with "..."), and any
// other source is shown as [string "first line..."], truncated to LUA_IDSIZE.
func shortSrc(source string) string {
	idSize := IDSize                      // LUA_IDSIZE (luaconf.go)
	if source != "" && source[0] == '=' { // literal
		if len(source) <= idSize {
			return source[1:]
		}
		return source[1:idSize] // truncate, no marker
	}
	if source != "" && source[0] == '@' { // file name
		if len(source) <= idSize {
			return source[1:]
		}
		return "..." + source[len(source)-(idSize-3-1):] // keep the tail
	}
	// string source, formatted as [string "..."]
	const pre, rets, pos = `[string "`, "...", `"]`
	bufflen := idSize - (len(pre) + len(rets) + len(pos) + 1)
	nl := strings.IndexByte(source, '\n')
	if len(source) < bufflen && nl < 0 {
		return pre + source + pos
	}
	end := len(source)
	if nl >= 0 {
		end = nl
	}
	if end > bufflen {
		end = bufflen
	}
	return pre + source[:end] + rets + pos
}

// typeError raises the standard "attempt to <op> a <type> value" error,
// appending a " (kind 'name')" suffix when the operand register is known.
// callError raises "attempt to call a X value", attributing the callee's name
// from the current instruction (PUC luaG_callerror via funcnamefromcode) rather
// than from the operand register, so a failed metamethod call reports e.g.
// "(metamethod 'add')" and a plain bad call reports "(global 'f')".
func (L *LState) callError(v Value) {
	info := ""
	if L.ci != nil && L.ci.isLuaFrame() {
		if cl := L.stack[L.ci.fn].closure(); cl != nil && cl.isLua() {
			if kind, name := funcNameFromCode(cl.proto, L.ci.savedpc-1); kind != "" {
				info = " (" + kind + " '" + name + "')"
			}
		}
	}
	L.errReg = -1
	L.errUpval = -1
	L.runtimeError("attempt to call a %s value%s", L.objTypeName(v), info)
}

func (L *LState) typeError(v Value, op string) {
	info := L.nameInfo(L.errReg)
	if info == "" {
		info = L.upvalNameInfo(L.errUpval)
	}
	L.errReg = -1
	L.errUpval = -1
	L.runtimeError("attempt to %s a %s value%s", op, L.objTypeName(v), info)
}
