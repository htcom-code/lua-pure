package luapure

import (
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

// The basic library (lbaselib.c): global functions always available.

// OpenBase installs the base library into the globals table.
func (L *LState) OpenBase() {
	g := L.globals
	setFuncs(g, map[string]GoFunc{
		"print":        basePrint,
		"type":         baseType,
		"tostring":     baseToString,
		"tonumber":     baseToNumber,
		"ipairs":       baseIpairs,
		"next":         baseNext,
		"pairs":        basePairs,
		"select":       baseSelect,
		"rawget":       baseRawget,
		"rawset":       baseRawset,
		"rawequal":     baseRawequal,
		"rawlen":       baseRawlen,
		"setmetatable": baseSetmetatable,
		"getmetatable": baseGetmetatable,
		"assert":       baseAssert,
		"error":        baseError,
		"pcall":          basePcall,
		"xpcall":         baseXpcall,
		"collectgarbage": baseCollectgarbage,
		"load":           baseLoad,
		"loadfile":       baseLoadfile,
		"dofile":         baseDofile,
		"warn":           baseWarn,
	})
	g.rawset(MkString("_G"), mkTable(g))
	g.rawset(MkString("_VERSION"), MkString("Lua 5.4"))
}

func basePrint(L *LState) int {
	n := L.NArgs()
	var sb strings.Builder
	for i := 1; i <= n; i++ {
		if i > 1 {
			sb.WriteByte('\t')
		}
		sb.WriteString(L.tostring(L.Arg(i)))
	}
	sb.WriteByte('\n')
	os.Stdout.WriteString(sb.String())
	return 0
}

func baseType(L *LState) int {
	if L.NArgs() < 1 {
		L.argError(1, "value expected")
	}
	L.Push(MkString(typeName(L.Arg(1))))
	return 1
}

func baseToString(L *LState) int {
	L.Push(MkString(L.tostring(L.checkAny(1))))
	return 1
}

func baseToNumber(L *LState) int {
	if L.NArgs() >= 2 && !L.Arg(2).IsNil() {
		// tonumber(s, base): the value must be a real string (no number coercion)
		base := L.checkInt(2)
		if !L.Arg(1).IsString() {
			L.typeArgError(1, "string")
		}
		s := strings.TrimSpace(L.Arg(1).Str())
		if base < 2 || base > 36 {
			L.argError(2, "base out of range")
		}
		neg := false
		if strings.HasPrefix(s, "-") {
			neg, s = true, s[1:]
		} else if strings.HasPrefix(s, "+") {
			s = s[1:]
		}
		if s == "" {
			L.Push(Nil)
			return 1
		}
		var n int64
		for i := 0; i < len(s); i++ {
			d := digitVal(s[i])
			if d < 0 || int64(d) >= base {
				L.Push(Nil)
				return 1
			}
			n = n*base + int64(d)
		}
		if neg {
			n = -n
		}
		L.Push(Int(n))
		return 1
	}
	v := L.Arg(1)
	if v.IsNumber() {
		L.Push(v)
		return 1
	}
	if v.IsString() {
		if num, ok := str2num(v.Str()); ok {
			L.Push(num)
			return 1
		}
	}
	L.checkAny(1) // there must be some argument: tonumber() -> "value expected"
	L.Push(Nil)
	return 1
}

func digitVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	}
	return -1
}

func baseNext(L *LState) int {
	t := L.checkTable(1)
	key := L.Arg(2)
	nk, nv, ok, found := t.next(key)
	if !found {
		L.errorf("invalid key to 'next'") // luaH_next: a plain runtime error
	}
	if !ok {
		L.Push(Nil)
		return 1
	}
	L.Push(nk)
	L.Push(nv)
	return 2
}

var nextFunc = NewGoFunc("next", baseNext)

func basePairs(L *LState) int {
	t := L.checkAny(1)
	if mt := L.metatableOf(t); mt != nil {
		if f := mt.rawgetStr("__pairs"); !f.IsNil() {
			res := L.CallValue(f, []Value{t}, 3)
			for i := 0; i < 3; i++ {
				if i < len(res) {
					L.Push(res[i])
				} else {
					L.Push(Nil)
				}
			}
			return 3
		}
	}
	L.checkTable(1)
	L.Push(nextFunc)
	L.Push(t)
	L.Push(Nil)
	return 3
}

// ipairsIter is a single shared iterator so that ipairs{} == ipairs{} holds
// (PUC hands out the same C function each call).
var ipairsIter = NewGoFunc("ipairs_iter", ipairsAux)

func baseIpairs(L *LState) int {
	t := L.checkAny(1)
	L.Push(ipairsIter)
	L.Push(t)
	L.Push(Int(0))
	return 3
}

func ipairsAux(L *LState) int {
	t := L.Arg(1)
	i := L.checkInt(2) + 1
	// PUC ipairsaux always uses lua_geti, so __index is honoured even for a
	// table that carries a metatable.
	v := L.indexGet(t, Int(i))
	if v.IsNil() {
		L.Push(Nil)
		return 1
	}
	L.Push(Int(i))
	L.Push(v)
	return 2
}

func baseSelect(L *LState) int {
	if L.Arg(1).IsString() && L.Arg(1).Str() == "#" {
		L.Push(Int(int64(L.NArgs() - 1)))
		return 1
	}
	n := L.checkInt(1)
	total := int64(L.NArgs() - 1)
	if n < 0 {
		n = total + n + 1
	}
	if n < 1 {
		L.argError(1, "index out of range")
	}
	cnt := 0
	for i := n; i <= total; i++ {
		L.Push(L.Arg(int(i) + 1))
		cnt++
	}
	return cnt
}

func baseRawget(L *LState) int {
	t := L.checkTable(1)
	L.Push(t.rawget(L.checkAny(2)))
	return 1
}

func baseRawset(L *LState) int {
	t := L.checkTable(1)
	key := L.checkAny(2)
	if key.IsNil() {
		L.argError(2, "table index is nil")
	}
	if key.IsFloat() && math.IsNaN(key.AsFloat()) {
		L.argError(2, "table index is NaN")
	}
	t.rawset(key, L.checkAny(3))
	L.Push(L.Arg(1))
	return 1
}

func baseRawequal(L *LState) int {
	L.Push(Bool(L.checkAny(1).RawEqual(L.checkAny(2))))
	return 1
}

func baseRawlen(L *LState) int {
	v := L.Arg(1)
	switch {
	case v.IsTable():
		L.Push(Int(v.tablev().length()))
	case v.IsString():
		L.Push(Int(int64(len(v.Str()))))
	default:
		L.argError(1, "table or string expected")
	}
	return 1
}

func baseSetmetatable(L *LState) int {
	t := L.checkTable(1)
	mt := L.Arg(2)
	if t.meta != nil && !t.meta.rawgetStr("__metatable").IsNil() {
		L.errorf("cannot change a protected metatable")
	}
	switch {
	case mt.IsNil():
		t.meta = nil
	case mt.IsTable():
		t.meta = mt.tablev()
	default:
		L.argError(2, "nil or table expected")
	}
	t.refreshWeak()            // pick up / drop __mode weakness
	L.checkFinalizer(L.Arg(1)) // attach a __gc finalizer if present
	L.Push(L.Arg(1))
	return 1
}

func baseGetmetatable(L *LState) int {
	mt := L.metatableOf(L.checkAny(1))
	if mt == nil {
		L.Push(Nil)
		return 1
	}
	if protected := mt.rawgetStr("__metatable"); !protected.IsNil() {
		L.Push(protected)
		return 1
	}
	L.Push(mkTable(mt))
	return 1
}

func baseAssert(L *LState) int {
	if L.checkAny(1).IsFalsy() {
		if L.NArgs() >= 2 {
			L.throw(L.Arg(2))
		}
		L.errorf("assertion failed!")
	}
	// return all arguments
	n := L.NArgs()
	for i := 1; i <= n; i++ {
		L.Push(L.Arg(i))
	}
	return n
}

func baseError(L *LState) int {
	v := L.Arg(1)
	level := L.optInt(2, 1)
	if v.IsString() && level > 0 {
		v = MkString(L.where(int(level)) + v.Str())
	}
	L.throw(v)
	return 0
}

func basePcall(L *LState) int {
	if L.NArgs() < 1 {
		L.argError(1, "value expected")
	}
	f := L.Arg(1)
	args := make([]Value, L.NArgs()-1)
	for i := range args {
		args[i] = L.Arg(i + 2)
	}
	// pcall has no message handler: clear any outer xpcall handler for this
	// protected region (PUC lua_pcall with msgh == 0).
	results, errv, ok := L.protectWith(f, args, Nil)
	if ok {
		L.Push(True)
		for _, r := range results {
			L.Push(r)
		}
		return 1 + len(results)
	}
	L.Push(False)
	L.Push(errv)
	return 2
}

func baseXpcall(L *LState) int {
	if L.NArgs() < 2 {
		L.argError(2, "value expected")
	}
	f := L.Arg(1)
	handler := L.Arg(2)
	if !handler.IsFunction() { // PUC: luaL_checktype(L, 2, LUA_TFUNCTION)
		L.typeArgError(2, "function")
	}
	args := make([]Value, L.NArgs()-2)
	for i := range args {
		args[i] = L.Arg(i + 3)
	}
	// The handler is installed as the message handler and runs at the error
	// point (live stack), so its result is already the error value here.
	results, errv, ok := L.protectWith(f, args, handler)
	if ok {
		L.Push(True)
		for _, r := range results {
			L.Push(r)
		}
		return 1 + len(results)
	}
	L.Push(False)
	L.Push(errv)
	return 2
}

// warnEnabled tracks the warning-system on/off state (default off), toggled by
// warn("@on")/warn("@off") (luaB_warn / lua_warning).
var warnEnabled = false

func baseWarn(L *LState) int {
	n := L.NArgs()
	L.checkString(1) // at least one argument, all must be strings
	for i := 2; i <= n; i++ {
		L.checkString(i)
	}
	first := L.Arg(1).Str()
	if n == 1 && len(first) > 0 && first[0] == '@' { // a control message
		switch first {
		case "@on":
			warnEnabled = true
		case "@off":
			warnEnabled = false
		}
		return 0
	}
	if warnEnabled {
		var sb strings.Builder
		for i := 1; i <= n; i++ {
			sb.WriteString(L.Arg(i).Str())
		}
		fmt.Fprintf(os.Stderr, "Lua warning: %s\n", sb.String())
	}
	return 0
}

func baseLoadfile(L *LState) int {
	fname := ""
	if L.NArgs() >= 1 && !L.Arg(1).IsNil() {
		fname = L.checkString(1)
	}
	var data []byte
	var err error
	if fname == "" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(fname)
	}
	if err != nil {
		L.Push(Nil)
		L.Push(MkString("cannot open " + fname))
		return 2
	}
	chunkname := "=stdin"
	if fname != "" {
		chunkname = "@" + fname
	}
	p, cerr := CompileString(string(data), chunkname)
	if cerr != nil {
		L.Push(Nil)
		L.Push(MkString(cerr.Error()))
		return 2
	}
	env := mkTable(L.globals)
	if L.NArgs() >= 3 && !L.Arg(3).IsNil() {
		env = L.Arg(3)
	}
	L.Push(L.loadProtoEnv(p, env))
	return 1
}

// gcModeName tracks the (cosmetic) GC mode for collectgarbage's mode switches;
// gopher delegates collection to the Go runtime, so values are placeholders.
var gcModeName = "incremental"

func baseCollectgarbage(L *LState) int {
	opt := L.checkOption(1, "collect", []string{"stop", "restart", "collect",
		"count", "step", "setpause", "setstepmul", "isrunning", "generational", "incremental"})
	switch opt {
	case "count":
		L.Push(Float(0)) // one value: Kbytes in use
		return 1
	case "step":
		// gopher delegates collection to the Go runtime; force a cycle so weak
		// tables observe reclaimed referents and __gc finalizers run (PUC does
		// both on a step). A forced runtime.GC() always completes a full cycle,
		// so report true (a cycle finished), matching PUC's step return — this
		// terminates `repeat until collectgarbage("step")` loops.
		L.finalizeAll()
		L.Push(True)
		return 1
	case "isrunning":
		L.Push(True)
		return 1
	case "setpause", "setstepmul":
		L.Push(Int(0)) // previous value
		return 1
	case "generational", "incremental":
		prev := gcModeName
		gcModeName = opt
		L.Push(MkString(prev))
		return 1
	default: // stop, restart, collect
		if opt == "collect" {
			// Force a full Go GC cycle so weak tables drop reclaimed referents
			// and __gc finalizers run, the way collectgarbage() does in PUC.
			L.finalizeAll()
		}
		L.Push(Int(0))
		return 1
	}
}
