package luapure

import "fmt"

// Auxiliary library helpers (lauxlib.c) used by the standard libraries: typed
// argument checking, error helpers, and the canonical value->string rendering.

// where returns a "source:line: " prefix for the function `level` frames above
// the current native call (lauxlib.c luaL_where).
func (L *LState) where(level int) string {
	ci := L.ci
	for i := 0; i < level && ci != nil; i++ {
		ci = ci.prev
	}
	if ci != nil && ci.isLuaFrame() {
		if cl := L.stack[ci.fn].closure(); cl != nil && cl.isLua() {
			return fmt.Sprintf("%s:%d: ", shortSrc(cl.proto.Source), cl.proto.LineAt(ci.savedpc-1))
		}
	}
	return ""
}

// errorf raises a string error with the caller's position prefix (luaL_error).
func (L *LState) errorf(format string, args ...interface{}) {
	L.throw(MkString(L.where(1) + fmt.Sprintf(format, args...)))
}

// getFuncName resolves how the current native function was named by its caller
// (getfuncname → funcnamefromcall): if the caller is a Lua frame, the call-site
// name; otherwise ("","") so the global-name fallback applies.
func (L *LState) getFuncName() (namewhat, name string) {
	return L.funcNameForCI(L.ci)
}

// funcNameForCI resolves the call-site name of the function running in frame ci,
// by examining the calling Lua frame's instruction (funcnamefromcall).
func (L *LState) funcNameForCI(ci *callInfo) (namewhat, name string) {
	if ci == nil {
		return "", ""
	}
	if ci.status&cistHook != 0 { // running as a debug hook (funcnamefromcall)
		return "hook", "?"
	}
	if ci.status&cistFin != 0 { // running as a __gc finalizer (funcnamefromcall)
		return "metamethod", "__gc"
	}
	caller := ci.prev
	if caller == nil || !caller.isLuaFrame() {
		return "", ""
	}
	cl := L.stack[caller.fn].closure()
	if cl == nil || !cl.isLua() {
		return "", ""
	}
	return funcNameFromCode(cl.proto, caller.savedpc-1)
}

// globalFuncName implements pushglobalfuncname: locate the running native
// function in package.loaded and return its qualified name ("string.rep"),
// dropping the "_G." prefix for globals. "" if not found.
func (L *LState) globalFuncName() string {
	if L.ci == nil {
		return ""
	}
	return L.globalFuncNameOf(L.stack[L.ci.fn])
}

// globalFuncNameOf is pushglobalfuncname for an arbitrary function value: it
// searches package.loaded (one level into each module) for fn and returns its
// qualified name, dropping the "_G." prefix for globals. "" if not found.
func (L *LState) globalFuncNameOf(fn Value) string {
	if L.pkgLoaded == nil || fn.tag != tagFunction {
		return ""
	}
	mk := Nil
	for {
		modName, mod, ok, _ := L.pkgLoaded.next(mk)
		if !ok {
			break
		}
		mk = modName
		if !mod.IsTable() || !modName.IsString() {
			continue
		}
		fk := Nil
		for {
			k, v, ok2, _ := mod.tablev().next(fk)
			if !ok2 {
				break
			}
			fk = k
			if v.tag == tagFunction && v.gc == fn.gc && k.IsString() {
				if modName.Str() == "_G" {
					return k.Str()
				}
				return modName.Str() + "." + k.Str()
			}
		}
	}
	return ""
}

// argError raises a "bad argument" error for argument n (luaL_argerror): a
// method's bad self argument becomes "calling '%s' on bad self", and the
// function is named from the call site, else from package.loaded.
func (L *LState) argError(n int, extra string) {
	namewhat, name := L.getFuncName()
	if namewhat == "method" {
		n-- // do not count self
		if n == 0 {
			L.errorf("calling '%s' on bad self (%s)", name, extra)
			return
		}
	}
	if name == "" {
		name = L.globalFuncName()
	}
	if name == "" {
		name = "?"
	}
	L.errorf("bad argument #%d to '%s' (%s)", n, name, extra)
}

func (L *LState) typeArgError(n int, want string) {
	got := L.argTypeName(L.Arg(n))
	if n > L.NArgs() {
		got = "no value"
	}
	L.argError(n, want+" expected, got "+got)
}

// argTypeName names a value for "bad argument" messages the way PUC
// luaL_typeerror does: a string __name metafield wins, then light userdata is
// reported as such, otherwise the basic type name (e.g. a file handle reports
// "FILE*", a tagged table its __name, a light userdata "light userdata").
func (L *LState) argTypeName(v Value) string {
	if mt := L.metatableOf(v); mt != nil {
		if nm := mt.rawgetStr("__name"); nm.IsString() {
			return nm.Str()
		}
	}
	if v.tag == tagLightUserData {
		return "light userdata"
	}
	return typeName(v)
}

// --- typed argument accessors ---

func (L *LState) checkAny(n int) Value {
	if n > L.NArgs() {
		L.argError(n, "value expected")
	}
	return L.Arg(n)
}

// checkOption is luaL_checkoption: the string arg n must be one of opts (def
// used when absent/nil); otherwise raise "invalid option '%s'".
func (L *LState) checkOption(n int, def string, opts []string) string {
	var s string
	if def != "" && (n > L.NArgs() || L.Arg(n).IsNil()) {
		s = def
	} else {
		s = L.checkString(n)
	}
	for _, o := range opts {
		if o == s {
			return o
		}
	}
	L.argError(n, "invalid option '"+s+"'")
	return ""
}

func (L *LState) checkString(n int) string {
	v := L.Arg(n)
	if v.IsString() {
		return v.Str()
	}
	if v.IsNumber() { // numbers coerce to strings as arguments (luaL_checklstring)
		return numToString(v)
	}
	L.typeArgError(n, "string")
	return ""
}

func (L *LState) checkInt(n int) int64 {
	v := L.Arg(n)
	if i, ok := tointegerCvt(v); ok {
		return i
	}
	if v.IsNumber() {
		L.argError(n, "number has no integer representation")
	}
	L.typeArgError(n, "number")
	return 0
}

func (L *LState) checkNumber(n int) float64 {
	if f, ok := tonumberCvt(L.Arg(n)); ok {
		return f
	}
	L.typeArgError(n, "number")
	return 0
}

func (L *LState) checkTable(n int) *Table {
	v := L.Arg(n)
	if v.IsTable() {
		return v.tablev()
	}
	L.typeArgError(n, "table")
	return nil
}

// checkTableVal validates that argument n is a table and returns it as a Value,
// for callers that index it through metamethods (settable/indexGet).
func (L *LState) checkTableVal(n int) Value {
	v := L.Arg(n)
	if !v.IsTable() {
		L.typeArgError(n, "table")
	}
	return v
}

func (L *LState) optInt(n int, def int64) int64 {
	if n > L.NArgs() || L.Arg(n).IsNil() {
		return def
	}
	return L.checkInt(n)
}

// tostring renders v the way the base library's tostring / print do, honouring
// __tostring and __name (luaL_tolstring).
func (L *LState) tostring(v Value) string {
	if mt := L.metatableOf(v); mt != nil {
		if f := mt.rawgetStr("__tostring"); !f.IsNil() {
			res := L.CallValue(f, []Value{v}, 1)
			// luaL_tolstring accepts whatever lua_isstring accepts: a string or
			// a number (coerced to its decimal); anything else is an error.
			if len(res) > 0 {
				if res[0].IsString() {
					return res[0].Str()
				}
				if res[0].IsNumber() {
					return numToString(res[0])
				}
			}
			L.errorf("'__tostring' must return a string")
		}
		if nm := mt.rawgetStr("__name"); nm.IsString() && (v.IsTable() || v.tag == tagUserData) {
			return fmt.Sprintf("%s: %p", nm.Str(), v.gc)
		}
	}
	switch v.tag {
	case tagNil:
		return "nil"
	case tagTrue:
		return "true"
	case tagFalse:
		return "false"
	case tagInt, tagFloat:
		return numToString(v)
	case tagString:
		return v.Str()
	case tagTable:
		return fmt.Sprintf("table: %p", v.gc)
	case tagFunction:
		// PUC renders both Lua and C functions as "function: %p".
		return fmt.Sprintf("function: %p", v.gc)
	default:
		return fmt.Sprintf("%s: %p", typeName(v), v.gc)
	}
}

// protect runs fn(args...) under recover, returning its results or the raised
// protectWith runs fn protected with handler installed as the message handler
// (PUC luaD_pcall's errfunc): the handler is invoked at the error point by
// throw. pcall passes Nil to clear any outer handler for its region; xpcall
// passes its handler. The previous handler is restored afterwards.
func (L *LState) protectWith(fn Value, args []Value, handler Value) (results []Value, errv Value, ok bool) {
	saved := L.errfunc
	L.errfunc = handler
	defer func() { L.errfunc = saved }()
	return L.protect(fn, args)
}

// error value. It is the native-side equivalent of pcall.
func (L *LState) protect(fn Value, args []Value) (results []Value, errv Value, ok bool) {
	funcIdx := L.top
	savedTop := L.top
	savedCI := L.ci
	savedTBC := len(L.tbc)
	defer func() {
		if r := recover(); r != nil {
			le, isLE := r.(*luaError)
			if !isLE {
				panic(r)
			}
			L.closeUpvals(funcIdx)
			// Run any to-be-closed variables created during the call, with the
			// propagating error, before tearing the frame down (PUC luaD_pcall
			// → luaD_closeprotected). Keep L.top above the closing variables so
			// the __close call temporaries don't clobber their stack slots; only
			// trim afterwards. The handlers see pcall as their caller, so restore
			// ci first; a __close that raises replaces the error.
			L.ci = savedCI
			errVal := le.value
			if savedTBC < len(L.tbc) {
				if final := L.runCloses(funcIdx, le); final != nil {
					errVal = final.value
				}
				if savedTBC < len(L.tbc) {
					L.tbc = L.tbc[:savedTBC]
				}
			}
			L.ci = savedCI
			L.top = savedTop
			results, errv, ok = nil, errVal, false
		}
	}()
	L.push(fn)
	for _, a := range args {
		L.push(a)
	}
	L.call(funcIdx, multRet)
	results = make([]Value, L.top-funcIdx)
	copy(results, L.stack[funcIdx:L.top])
	L.top = savedTop
	return results, Nil, true
}

// registerTable installs t as a global and, once the package library is open,
// records it in package.loaded so require(name) returns the same table.
func (L *LState) registerTable(name string, t *Table) {
	L.SetGlobal(name, mkTable(t))
	if L.pkgLoaded != nil {
		L.pkgLoaded.rawset(MkString(name), mkTable(t))
	}
}

// setFuncs installs the given native functions as fields of table t.
func setFuncs(t *Table, funcs map[string]GoFunc) {
	for name, fn := range funcs {
		t.rawset(MkString(name), NewGoFunc(name, fn))
	}
}
