package luapure

import (
	"fmt"
	"strings"
	"unsafe"
)

// The debug library (ldblib.c): introspection over the call stack, locals,
// upvalues and metatables, plus hook installation. getinfo's name/namewhat
// analysis is approximated (the calling-instruction decode PUC does is omitted).

func (L *LState) OpenDebug() {
	t := newTable()
	setFuncs(t, map[string]GoFunc{
		"getinfo":      dbgGetinfo,
		"traceback":    dbgTraceback,
		"sethook":      dbgSethook,
		"gethook":      dbgGethook,
		"getlocal":     dbgGetlocal,
		"setlocal":     dbgSetlocal,
		"getupvalue":   dbgGetupvalue,
		"setupvalue":   dbgSetupvalue,
		"upvalueid":    dbgUpvalueid,
		"upvaluejoin":  dbgUpvaluejoin,
		"getmetatable": dbgGetmetatable,
		"setmetatable": dbgSetmetatable,
		"getregistry":  dbgGetregistry,
		"getuservalue": dbgGetuservalue,
		"setuservalue": dbgSetuservalue,
		"traceback2":   dbgTraceback,
		"debug":        dbgDebug,
	})
	L.registerTable("debug", t)
}

// getThreadArg implements ldblib.c's getthread: if argument 1 is a thread,
// operate on it and report an argument offset of 1; otherwise operate on the
// current thread with no offset.
func (L *LState) getThreadArg() (target *LState, off int) {
	if L.Arg(1).IsThread() {
		return L.Arg(1).threadv(), 1
	}
	return L, 0
}

// ciAtLevel returns the call frame `level` steps above the current native call
// (level 0 = this C function, 1 = its caller, ...).
func (L *LState) ciAtLevel(level int) *callInfo {
	if level < 0 {
		return nil // PUC lua_getstack: a negative level is out of range
	}
	ci := L.ci
	for i := 0; i < level && ci != nil; i++ {
		ci = ci.prev
	}
	return ci
}

// frameLimit returns the upper bound (exclusive stack index) of ci's value
// region: L.top for the running frame, else the inner (callee) frame's function
// slot (PUC luaG_findlocal's `(ci==L->ci) ? L->top : ci->next->func`).
func (L *LState) frameLimit(ci *callInfo) int {
	if ci == L.ci {
		return L.top
	}
	for x := L.ci; x != nil; x = x.prev {
		if x.prev == ci {
			return x.fn
		}
	}
	return L.top
}

// findLocal resolves the n-th local of frame ci (PUC luaG_findlocal): a named
// Lua local, a vararg (negative n), or an unnamed in-range stack slot reported
// as "(temporary)" / "(C temporary)". ok=false means n is out of range.
func (tL *LState) findLocal(ci *callInfo, n int) (name string, slot int, ok bool) {
	if ci.isLuaFrame() {
		if cl := tL.stack[ci.fn].closure(); cl != nil && cl.isLua() {
			if n < 0 { // vararg access
				if pos, vok := varargSlot(tL, ci, cl, -n); vok {
					return "(vararg)", pos, true
				}
				return "", 0, false
			}
			if nm := localName(cl.proto, n, ci.savedpc-1); nm != "" {
				return nm, ci.base + n - 1, true
			}
		}
		if n >= 1 && ci.base+n-1 < tL.frameLimit(ci) {
			return "(temporary)", ci.base + n - 1, true
		}
		return "", 0, false
	}
	if n >= 1 && ci.base+n-1 < tL.frameLimit(ci) {
		return "(C temporary)", ci.base + n - 1, true
	}
	return "", 0, false
}

const dbgValidOptions = "nSluftLr"

func dbgGetinfo(L *LState) int {
	// Optional leading thread argument: debug.getinfo([thread,] f, [what]).
	tL := L
	arg := 1
	if L.Arg(1).IsThread() {
		tL = L.Arg(1).threadv()
		arg = 2
	}

	var ci *callInfo
	var fn Value
	what := "nSltufr" // PUC default options (no L)
	if L.Arg(arg).IsNumber() {
		level := int(L.checkInt(arg))
		if level < 0 {
			L.Push(Nil) // out-of-range level
			return 1
		}
		ci = tL.ciAtLevel(level)
		if ci == nil {
			L.Push(Nil)
			return 1
		}
		fn = tL.stack[ci.fn]
	} else if L.Arg(arg).IsFunction() {
		fn = L.Arg(arg)
	} else {
		L.typeArgError(arg, "function or level")
	}
	if L.Arg(arg + 1).IsString() {
		what = L.Arg(arg + 1).Str()
	}
	for _, c := range what {
		if !strings.ContainsRune(dbgValidOptions, c) {
			L.argError(arg+1, "invalid option")
		}
	}

	info := newTable()
	cl := fn.closure()
	isLua := cl != nil && cl.isLua()

	set := func(k string, v Value) { info.rawset(MkString(k), v) }
	if strings.ContainsRune(what, 'S') {
		if isLua {
			src := cl.proto.Source
			set("source", MkString(src))
			set("short_src", MkString(shortSrc(src)))
			set("linedefined", Int(int64(cl.proto.LineDefined)))
			set("lastlinedefined", Int(int64(cl.proto.LastLineDef)))
			if cl.proto.LineDefined == 0 {
				set("what", MkString("main"))
			} else {
				set("what", MkString("Lua"))
			}
		} else {
			set("source", MkString("=[C]"))
			set("short_src", MkString("[C]"))
			set("linedefined", Int(-1))
			set("lastlinedefined", Int(-1))
			set("what", MkString("C"))
		}
	}
	if strings.ContainsRune(what, 'l') {
		if isLua && ci != nil {
			set("currentline", Int(int64(cl.proto.LineAt(ci.savedpc-1))))
		} else {
			set("currentline", Int(-1))
		}
	}
	if strings.ContainsRune(what, 'u') {
		if isLua {
			set("nups", Int(int64(len(cl.upvals))))
			set("nparams", Int(int64(cl.proto.NumParams)))
			set("isvararg", Bool(cl.proto.IsVararg))
		} else {
			set("nups", Int(int64(len(cl.goUpvals))))
			set("nparams", Int(0))
			set("isvararg", True)
		}
	}
	if strings.ContainsRune(what, 'n') {
		// Name comes from the call site, so only a level (not a bare function)
		// can carry one (funcnamefromcall).
		set("name", Nil)
		set("namewhat", MkString(""))
		if ci != nil {
			if nw, nm := tL.funcNameForCI(ci); nw != "" {
				set("name", MkString(nm))
				set("namewhat", MkString(nw))
			}
		}
	}
	if strings.ContainsRune(what, 't') {
		set("istailcall", Bool(ci != nil && ci.status&cistTail != 0))
	}
	if strings.ContainsRune(what, 'r') {
		// ftransfer/ntransfer are only meaningful inside a call/return hook,
		// where the firing recorded them on the frame; 0 otherwise (PUC).
		if ci != nil {
			set("ftransfer", Int(int64(ci.ftransfer)))
			set("ntransfer", Int(int64(ci.ntransfer)))
		} else {
			set("ftransfer", Int(0))
			set("ntransfer", Int(0))
		}
	}
	if strings.ContainsRune(what, 'L') {
		if isLua {
			set("activelines", mkTable(activeLines(cl.proto)))
		} else {
			set("activelines", Nil)
		}
	}
	if strings.ContainsRune(what, 'f') {
		set("func", fn)
	}
	L.Push(mkTable(info))
	return 1
}

// activeLines builds the set of source lines that carry code (collectvalidlines):
// a table mapping each instruction's line to true. Empty when line info was
// stripped.
func activeLines(p *Proto) *Table {
	t := newTable()
	pc := 0
	// PUC collectvalidlines skips a vararg function's leading OP_VARARGPREP, so
	// the function-definition line is not reported as active unless a real
	// instruction lands on it.
	if p.IsVararg && len(p.Code) > 0 && GetOpCode(p.Code[0]) == OP_VARARGPREP {
		pc = 1
	}
	for ; pc < len(p.LineInfo); pc++ {
		t.rawset(Int(int64(p.LineInfo[pc])), True)
	}
	return t
}

func dbgTraceback(L *LState) int {
	// debug.traceback([thread,] [message [, level]]): a non-string, non-nil
	// message is returned unchanged.
	tL, off := L.getThreadArg()
	msg := L.Arg(off + 1)
	if !msg.IsNil() && !msg.IsString() && !msg.IsNumber() {
		L.Push(msg)
		return 1
	}
	defLevel := int64(1)
	if tL != L {
		defLevel = 0 // another thread is traced from its top
	}
	level := int(L.optInt(off+2, defLevel))
	var sb strings.Builder
	if msg.IsString() || msg.IsNumber() {
		sb.WriteString(tostr(msg))
		sb.WriteByte('\n')
	}
	sb.WriteString("stack traceback:")
	// Collect the frames from `level` down, then emit them with PUC's size
	// truncation: when there are more than LEVELS1+LEVELS2 frames, show the
	// first LEVELS1, a "(skipping N levels)" marker, and the last LEVELS2.
	const levels1, levels2 = 10, 11
	var frames []*callInfo
	for c := tL.ciAtLevel(level); c != nil; c = c.prev {
		frames = append(frames, c)
	}
	total := len(frames)
	for i := 0; i < total; i++ {
		if total > levels1+levels2 && i == levels1 {
			sb.WriteString(fmt.Sprintf("\n\t...\t(skipping %d levels)", total-levels1-levels2))
			i = total - levels2 - 1 // continue from the last LEVELS2 frames
			continue
		}
		sb.WriteString("\n\t")
		tL.traceFrame(&sb, frames[i])
	}
	L.Push(MkString(sb.String()))
	return 1
}

// traceFrame writes one traceback line for ci (luaL_traceback / pushfuncname).
func (tL *LState) traceFrame(sb *strings.Builder, ci *callInfo) {
	// pushfuncname: a qualified name from package.loaded wins over the call-site
	// name (PUC tries pushglobalfuncname first).
	gname := tL.globalFuncNameOf(tL.stack[ci.fn])
	nw, nm := tL.funcNameForCI(ci) // call-site name (funcnamefromcall)
	if ci.isLuaFrame() {
		cl := tL.stack[ci.fn].closure()
		if cl != nil && cl.isLua() {
			src := shortSrc(cl.proto.Source)
			line := cl.proto.LineAt(ci.savedpc - 1)
			var desc string
			switch {
			case gname != "":
				desc = "function '" + gname + "'"
			case nw != "":
				desc = nw + " '" + nm + "'"
			case cl.proto.LineDefined == 0:
				desc = "main chunk"
			default:
				desc = fmt.Sprintf("function <%s:%d>", src, cl.proto.LineDefined)
			}
			if line > 0 {
				sb.WriteString(fmt.Sprintf("%s:%d: in %s", src, line, desc))
			} else {
				sb.WriteString(fmt.Sprintf("%s: in %s", src, desc))
			}
		} else {
			sb.WriteString("[C]: in ?")
		}
		return
	}
	cl := tL.stack[ci.fn].closure()
	switch {
	case gname != "":
		sb.WriteString("[C]: in function '" + gname + "'")
	case nw != "":
		sb.WriteString("[C]: in " + nw + " '" + nm + "'")
	case cl != nil && cl.name != "":
		sb.WriteString("[C]: in function '" + cl.name + "'")
	default:
		sb.WriteString("[C]: in ?")
	}
}

// hookTable returns registry._HOOKKEY, the table PUC uses to map each thread to
// its hook function (ldblib HOOKKEY). It is its own metatable with __mode='k',
// so it has weak keys and getmetatable(it).__mode == 'k'. create=false returns
// nil when it does not exist yet.
func (L *LState) hookTable(create bool) *Table {
	if ht := L.registry.rawgetStr("_HOOKKEY"); ht.IsTable() {
		return ht.tablev()
	}
	if !create {
		return nil
	}
	ht := newTable()
	ht.rawset(MkString("__mode"), MkString("k"))
	ht.meta = ht // the hook table is its own metatable (PUC)
	ht.refreshWeak()
	L.registry.rawset(MkString("_HOOKKEY"), mkTable(ht))
	return ht
}

func dbgSethook(L *LState) int {
	tL, off := L.getThreadArg()
	if L.NArgs() <= off || L.Arg(off+1).IsNil() {
		tL.hook = Nil
		tL.hookMask = 0
		tL.hookCount = 0
		if ht := L.hookTable(false); ht != nil {
			ht.rawset(mkThread(tL), Nil) // drop this thread's hook entry
		}
		return 0
	}
	f := L.Arg(off + 1)
	maskStr := L.checkString(off + 2)
	count := int(L.optInt(off+3, 0))
	mask := 0
	if strings.ContainsRune(maskStr, 'c') {
		mask |= maskCall
	}
	if strings.ContainsRune(maskStr, 'r') {
		mask |= maskRet
	}
	if strings.ContainsRune(maskStr, 'l') {
		mask |= maskLine
	}
	if count > 0 {
		mask |= maskCount
	}
	tL.hook = f
	tL.hookMask = mask
	tL.hookCount = count
	tL.hookCdown = count
	L.hookTable(true).rawset(mkThread(tL), f) // registry._HOOKKEY[thread] = hook
	// Installing a line hook mid-function must not re-fire the line the hook was
	// set on: sync the resuming (caller) frame's last line, the way PUC's oldpc
	// already points at the current instruction.
	if mask&maskLine != 0 && tL == L && L.ci != nil {
		if caller := L.ci.prev; caller != nil && caller.isLuaFrame() {
			if cl := L.stack[caller.fn].closure(); cl != nil && cl.isLua() {
				caller.lastLine = cl.proto.LineAt(caller.savedpc - 1)
				caller.lastHkPc = caller.savedpc - 1
			}
		}
	}
	return 0
}

func dbgGethook(L *LState) int {
	tL, _ := L.getThreadArg()
	if tL.hook.IsNil() {
		L.Push(Nil)
		L.Push(MkString(""))
		L.Push(Int(0))
		return 3
	}
	var mask strings.Builder
	if tL.hookMask&maskCall != 0 {
		mask.WriteByte('c')
	}
	if tL.hookMask&maskRet != 0 {
		mask.WriteByte('r')
	}
	if tL.hookMask&maskLine != 0 {
		mask.WriteByte('l')
	}
	L.Push(tL.hook)
	L.Push(MkString(mask.String()))
	L.Push(Int(int64(tL.hookCount)))
	return 3
}

func dbgGetlocal(L *LState) int {
	tL, off := L.getThreadArg()
	// Function form: debug.getlocal([thread,] f, n) returns just the name of
	// the function's n-th local (its parameters at entry).
	if L.Arg(off + 1).IsFunction() {
		cl := L.Arg(off + 1).closure()
		n := int(L.checkInt(off + 2))
		if cl == nil || !cl.isLua() {
			L.Push(Nil)
			return 1
		}
		name := localName(cl.proto, n, 0)
		if name == "" {
			L.Push(Nil)
			return 1
		}
		L.Push(MkString(name))
		return 1
	}
	level := int(L.checkInt(off + 1))
	n := int(L.checkInt(off + 2))
	ci := tL.ciAtLevel(level)
	if ci == nil {
		L.argError(off+1, "level out of range")
	}
	name, slot, ok := tL.findLocal(ci, n)
	if !ok {
		L.Push(Nil)
		return 1
	}
	L.Push(MkString(name))
	L.Push(tL.stack[slot])
	return 2
}

// varargSlot returns the stack index of the vn-th (1-based) vararg of a Lua
// frame, like PUC findvararg: the extra args sit at ci.fn-nextra .. ci.fn-1.
func varargSlot(tL *LState, ci *callInfo, cl *Closure, vn int) (int, bool) {
	if !cl.proto.IsVararg || vn < 1 || vn > ci.nextra {
		return 0, false
	}
	return ci.fn - ci.nextra + (vn - 1), true
}

func dbgSetlocal(L *LState) int {
	tL, off := L.getThreadArg()
	level := int(L.checkInt(off + 1))
	n := int(L.checkInt(off + 2))
	v := L.checkAny(off + 3)
	ci := tL.ciAtLevel(level)
	if ci == nil {
		L.argError(off+1, "level out of range")
	}
	name, slot, ok := tL.findLocal(ci, n)
	if !ok {
		L.Push(Nil)
		return 1
	}
	tL.stack[slot] = v
	L.Push(MkString(name))
	return 1
}

// localName returns the name of the n-th local active at pc (luaF_getlocalname).
func localName(p *Proto, n, pc int) string {
	for i := 0; i < len(p.LocVars) && p.LocVars[i].StartPc <= pc; i++ {
		if pc < p.LocVars[i].EndPc {
			n--
			if n == 0 {
				return p.LocVars[i].Name
			}
		}
	}
	return ""
}

func dbgGetupvalue(L *LState) int {
	cl := L.checkFunc(1)
	n := int(L.checkInt(2))
	name, v, ok := upvalAt(cl, n)
	if !ok {
		return 0 // PUC db_getupvalue returns no values for an out-of-range index
	}
	L.Push(MkString(name))
	L.Push(v)
	return 2
}

func dbgSetupvalue(L *LState) int {
	cl := L.checkFunc(1)
	n := int(L.checkInt(2))
	v := L.checkAny(3)
	// PUC db_setupvalue returns no values for an out-of-range index.
	if cl.isLua() {
		if n < 1 || n > len(cl.upvals) {
			return 0
		}
		cl.upvals[n-1].set(v)
		L.Push(MkString(upvalDisplayName(cl.proto.Upvalues[n-1].Name)))
		return 1
	}
	if n < 1 || n > len(cl.goUpvals) {
		return 0
	}
	cl.goUpvals[n-1] = v
	L.Push(MkString(""))
	return 1
}

func upvalAt(cl *Closure, n int) (string, Value, bool) {
	if cl.isLua() {
		if n < 1 || n > len(cl.upvals) {
			return "", Nil, false
		}
		return upvalDisplayName(cl.proto.Upvalues[n-1].Name), cl.upvals[n-1].get(), true
	}
	if n < 1 || n > len(cl.goUpvals) {
		return "", Nil, false
	}
	return "", cl.goUpvals[n-1], true
}

// upvalDisplayName is PUC aux_upvalue's name handling for Lua closures: an
// upvalue with no name (a stripped chunk drops names) reports as "(no name)".
func upvalDisplayName(name string) string {
	if name == "" {
		return "(no name)"
	}
	return name
}

func dbgUpvalueid(L *LState) int {
	cl := L.checkFunc(1)
	n := int(L.checkInt(2))
	// PUC db_upvalueid calls checkupval with pnup=NULL, so an out-of-range
	// index is NOT an error (unlike upvaluejoin): lua_upvalueid returns NULL
	// and db_upvalueid pushes nil.
	if cl.isLua() {
		if n < 1 || n > len(cl.upvals) {
			L.Push(Nil)
			return 1
		}
		L.Push(mkLightUserData(unsafe.Pointer(cl.upvals[n-1])))
		return 1
	}
	if n < 1 || n > len(cl.goUpvals) {
		L.Push(Nil)
		return 1
	}
	L.Push(mkLightUserData(unsafe.Pointer(&cl.goUpvals[n-1])))
	return 1
}

func dbgUpvaluejoin(L *LState) int {
	cl1 := L.checkFunc(1)
	n1 := int(L.checkInt(2))
	cl2 := L.checkFunc(3)
	n2 := int(L.checkInt(4))
	if !cl1.isLua() || !cl2.isLua() {
		L.argError(1, "Lua function expected")
	}
	if n1 < 1 || n1 > len(cl1.upvals) {
		L.argError(2, "invalid upvalue index")
	}
	if n2 < 1 || n2 > len(cl2.upvals) {
		L.argError(4, "invalid upvalue index")
	}
	cl1.upvals[n1-1] = cl2.upvals[n2-1]
	return 0
}

func dbgGetmetatable(L *LState) int {
	mt := L.metatableOf(L.checkAny(1))
	if mt == nil {
		L.Push(Nil)
		return 1
	}
	L.Push(mkTable(mt))
	return 1
}

func dbgSetmetatable(L *LState) int {
	v := L.checkAny(1)
	mt := L.Arg(2)
	var m *Table
	if mt.IsTable() {
		m = mt.tablev()
	} else if !mt.IsNil() {
		L.argError(2, "nil or table expected")
	}
	switch v.tag {
	case tagTable:
		tbl := v.tablev()
		tbl.meta = m
		tbl.refreshWeak()
		L.checkFinalizer(v)
	case tagUserData:
		v.userData().meta = m
		L.checkFinalizer(v)
	default:
		if t := v.Type(); t >= 0 && t < numTypeTags {
			L.basicMT[t] = m
		}
	}
	L.Push(v)
	return 1
}

func dbgGetregistry(L *LState) int {
	L.Push(mkTable(L.registry))
	return 1
}

func dbgGetuservalue(L *LState) int {
	v := L.Arg(1)
	n := int(L.optInt(2, 1))
	if v.IsUserData() {
		ud := v.userData()
		if n >= 1 && n <= len(ud.uv) {
			L.Push(ud.uv[n-1])
			L.Push(True)
			return 2
		}
	}
	L.Push(Nil)
	L.Push(False)
	return 2
}

func dbgSetuservalue(L *LState) int {
	// PUC db_setuservalue: luaL_checktype(1, LUA_TUSERDATA) first, so a light
	// userdata (e.g. from debug.upvalueid) errors with "got light userdata".
	n := int(L.optInt(3, 1))
	v := L.Arg(1)
	if !v.IsUserData() {
		L.typeArgError(1, "userdata")
	}
	val := L.checkAny(2)
	ud := v.userData()
	if n < 1 || n > len(ud.uv) {
		L.Push(Nil) // n out of range: lua_setiuservalue fails
		return 1
	}
	ud.uv[n-1] = val
	L.Push(v)
	return 1
}

func dbgDebug(L *LState) int { return 0 } // interactive prompt: no-op

// checkFunc returns the closure argument at position n.
func (L *LState) checkFunc(n int) *Closure {
	v := L.Arg(n)
	if v.IsFunction() {
		return v.closure()
	}
	L.typeArgError(n, "function")
	return nil
}
