package luapure

import (
	"math"
	"strings"
)

// The Lua 5.4 virtual machine: a faithful port of lvm.c's luaV_execute and its
// helpers (table access, concat, comparison, length, numeric-for preparation).
// Stack slots are addressed by absolute index; the C "goto startfunc/returning"
// control flow is modelled by an outer frame loop with labelled continue.

// MaxTagLoop (luaconf.go) limits __index/__newindex chains (lvm.c MAXTAGLOOP).

// --- table access (luaV_finishget / luaV_finishset, fast paths folded in) ---

// gettable performs val = t[key] with __index handling, storing into stack[ra].
func (L *LState) gettable(t, key Value, ra int) {
	for loop := 0; loop < MaxTagLoop; loop++ {
		var tm Value
		if t.IsTable() {
			tbl := t.tablev()
			if v := tbl.rawget(key); !v.IsNil() {
				L.stack[ra] = v
				return
			}
			if tbl.meta == nil {
				L.stack[ra] = Nil
				return
			}
			tm = tbl.meta.rawgetStr("__index")
			if tm.IsNil() {
				L.stack[ra] = Nil
				return
			}
		} else {
			tm = L.gettmByObj(t, tmIndex)
			if tm.IsNil() {
				L.typeError(t, "index")
			}
		}
		if tm.IsFunction() {
			L.callTMres(tm, t, key, ra)
			return
		}
		t = tm // repeat with tm as the new table
	}
	L.runtimeError("'__index' chain too long; possible loop")
}

// indexGet performs t[key] honoring the __index chain and returns the value
// (PUC lua_gettable). Used by library code that needs the result as a value
// rather than written into a register, e.g. table-replacement gsub.
func (L *LState) indexGet(t, key Value) Value {
	for loop := 0; loop < MaxTagLoop; loop++ {
		var tm Value
		if t.IsTable() {
			tbl := t.tablev()
			if v := tbl.rawget(key); !v.IsNil() {
				return v
			}
			if tbl.meta == nil {
				return Nil
			}
			tm = tbl.meta.rawgetStr("__index")
			if tm.IsNil() {
				return Nil
			}
		} else {
			tm = L.gettmByObj(t, tmIndex)
			if tm.IsNil() {
				L.typeError(t, "index")
			}
		}
		if tm.IsFunction() {
			res := L.CallValue(tm, []Value{t, key}, 1)
			if len(res) > 0 {
				return res[0]
			}
			return Nil
		}
		t = tm
	}
	L.runtimeError("'__index' chain too long; possible loop")
	return Nil
}

// settable performs t[key] = val with __newindex handling.
func (L *LState) settable(t, key, val Value) {
	for loop := 0; loop < MaxTagLoop; loop++ {
		var tm Value
		if t.IsTable() {
			tbl := t.tablev()
			if !tbl.rawget(key).IsNil() { // existing slot: plain assign
				tbl.rawset(key, val)
				return
			}
			if tbl.meta == nil {
				L.newindex(tbl, key, val)
				return
			}
			tm = tbl.meta.rawgetStr("__newindex")
			if tm.IsNil() {
				L.newindex(tbl, key, val)
				return
			}
		} else {
			tm = L.gettmByObj(t, tmNewIndex)
			if tm.IsNil() {
				L.typeError(t, "index")
			}
		}
		if tm.IsFunction() {
			L.callTM(tm, t, key, val)
			return
		}
		t = tm
	}
	L.runtimeError("'__newindex' chain too long; possible loop")
}

// newindex inserts a fresh key, validating it like luaH_newkey.
func (L *LState) newindex(tbl *Table, key, val Value) {
	if key.IsNil() {
		L.runtimeError("table index is nil")
	}
	if key.IsFloat() && math.IsNaN(key.AsFloat()) {
		L.runtimeError("table index is NaN")
	}
	// Cap unbounded array growth as a stand-in for PUC's malloc-failure path
	// (Go can't catch a real OOM): raise only when this key would extend the
	// array part past the ceiling, so non-array inserts and bounded tables are
	// unaffected.
	if MaxTableArraySize > 0 && len(tbl.arr) >= MaxTableArraySize {
		if k, ok := arrayIndex(key); ok && k == int64(len(tbl.arr))+1 && !val.IsNil() {
			L.runtimeError("not enough memory")
		}
	}
	tbl.rawset(key, val)
}

// --- concatenation (luaV_concat) ---

func concatable(v Value) bool { return v.IsString() || v.IsNumber() }

func tostr(v Value) string {
	if v.IsString() {
		return v.Str()
	}
	return numToString(v)
}

// concat folds the top `total` stack values into a single string at the bottom
// of that run, deferring to __concat when an operand is not string/number.
func (L *LState) concat(total int) {
	for total > 1 {
		top := L.top
		if !concatable(L.stack[top-2]) || !concatable(L.stack[top-1]) {
			L.tryconcatTM() // result left at top-2
			total--
			L.top = top - 1
			continue
		}
		n := 1
		for n < total && concatable(L.stack[top-n-1]) {
			n++
		}
		// Sum the parts first so the result length is checked for overflow
		// before allocating (luaV_concat raises "string length overflow" rather
		// than building an impossibly long string).
		parts := make([]string, n)
		size := 0
		for i := 0; i < n; i++ {
			s := tostr(L.stack[top-n+i])
			if len(s) > math.MaxInt-size {
				L.runtimeError("string length overflow")
			}
			size += len(s)
			parts[i] = s
		}
		var sb strings.Builder
		sb.Grow(size)
		for _, s := range parts {
			sb.WriteString(s)
		}
		L.stack[top-n] = MkString(sb.String())
		total -= n - 1
		L.top = top - (n - 1)
	}
}

// --- length (luaV_objlen) ---

func (L *LState) objlen(ra int, v Value) {
	switch {
	case v.IsString():
		L.stack[ra] = Int(int64(len(v.Str())))
	case v.IsTable():
		tbl := v.tablev()
		if tbl.meta != nil {
			if tm := tbl.meta.rawgetStr("__len"); !tm.IsNil() {
				L.callTMres(tm, v, v, ra)
				return
			}
		}
		L.stack[ra] = Int(tbl.length())
	default:
		tm := L.gettmByObj(v, tmLen)
		if tm.IsNil() {
			L.typeError(v, "get length of")
		}
		L.callTMres(tm, v, v, ra)
	}
}

// lenOf returns #v as an integer, honoring __len (PUC luaL_len): a metamethod
// result that is not an integer is an error. Used by the table library, whose
// functions operate through metamethods (lua_geti/lua_seti/luaL_len).
func (L *LState) lenOf(v Value) int64 {
	if v.IsString() {
		return int64(len(v.Str()))
	}
	var tm Value
	if v.IsTable() {
		tbl := v.tablev()
		if tbl.meta == nil {
			return tbl.length()
		}
		tm = tbl.meta.rawgetStr("__len")
		if tm.IsNil() {
			return tbl.length()
		}
	} else {
		tm = L.gettmByObj(v, tmLen)
		if tm.IsNil() {
			L.typeError(v, "get length of")
		}
	}
	res := L.CallValue(tm, []Value{v, v}, 1)
	var r Value
	if len(res) > 0 {
		r = res[0]
	}
	if i, ok := toIntegerNS(r); ok {
		return i
	}
	L.errorf("object length is not an integer")
	return 0
}

// --- comparison (LTnum/LEnum and the equal/order entry points) ---

func intFitsFloat(i int64) bool {
	const lim = int64(1) << 53
	return i >= -lim && i <= lim
}

func f2iFloor(f float64) (int64, bool) { return numberToInteger(math.Floor(f)) }
func f2iCeil(f float64) (int64, bool)  { return numberToInteger(math.Ceil(f)) }

func ltIntFloat(i int64, f float64) bool {
	if intFitsFloat(i) {
		return float64(i) < f
	}
	if fi, ok := f2iCeil(f); ok {
		return i < fi
	}
	return f > 0
}

func leIntFloat(i int64, f float64) bool {
	if intFitsFloat(i) {
		return float64(i) <= f
	}
	if fi, ok := f2iFloor(f); ok {
		return i <= fi
	}
	return f > 0
}

func ltFloatInt(f float64, i int64) bool {
	if intFitsFloat(i) {
		return f < float64(i)
	}
	if fi, ok := f2iFloor(f); ok {
		return fi < i
	}
	return f < 0
}

func leFloatInt(f float64, i int64) bool {
	if intFitsFloat(i) {
		return f <= float64(i)
	}
	if fi, ok := f2iCeil(f); ok {
		return fi <= i
	}
	return f < 0
}

func numLT(a, b Value) bool {
	if a.IsInt() {
		if b.IsInt() {
			return a.AsInt() < b.AsInt()
		}
		return ltIntFloat(a.AsInt(), b.AsFloat())
	}
	if b.IsFloat() {
		return a.AsFloat() < b.AsFloat()
	}
	return ltFloatInt(a.AsFloat(), b.AsInt())
}

func numLE(a, b Value) bool {
	if a.IsInt() {
		if b.IsInt() {
			return a.AsInt() <= b.AsInt()
		}
		return leIntFloat(a.AsInt(), b.AsFloat())
	}
	if b.IsFloat() {
		return a.AsFloat() <= b.AsFloat()
	}
	return leFloatInt(a.AsFloat(), b.AsInt())
}

// equalobj is the full equality, falling back to __eq for distinct tables
// (luaV_equalobj). RawEqual already covers primitives and mixed int/float.
func (L *LState) equalobj(a, b Value) bool {
	if a.RawEqual(b) {
		return true
	}
	// __eq is tried for two tables or two full userdata (luaV_equalobj).
	if (a.IsTable() && b.IsTable()) || (a.IsUserData() && b.IsUserData()) {
		tm := L.gettmByObj(a, tmEq)
		if tm.IsNil() {
			tm = L.gettmByObj(b, tmEq)
		}
		if tm.IsNil() {
			return false
		}
		res := L.scratchTop()
		L.callTMres(tm, a, b, res)
		return !L.stack[res].IsFalsy()
	}
	return false
}

func (L *LState) lessthan(a, b Value) bool {
	if a.IsNumber() && b.IsNumber() {
		return numLT(a, b)
	}
	if a.IsString() && b.IsString() {
		return a.Str() < b.Str()
	}
	return L.callorderTM(a, b, tmLt)
}

func (L *LState) lessequal(a, b Value) bool {
	if a.IsNumber() && b.IsNumber() {
		return numLE(a, b)
	}
	if a.IsString() && b.IsString() {
		return a.Str() <= b.Str()
	}
	return L.callorderTM(a, b, tmLe)
}

// --- numeric for (forprep / floatforloop / forlimit) ---

func (L *LState) forError(what string, o Value) {
	L.runtimeError("bad 'for' %s (number expected, got %s)", what, L.objTypeName(o))
}

// tointegerForLimit coerces a 'for' limit to an integer, rounding toward floor
// (ceil for descending loops), with string coercion (luaV_tointeger).
func tointegerForLimit(v Value, ceil bool) (int64, bool) {
	if v.IsString() {
		n, ok := str2num(v.Str())
		if !ok {
			return 0, false
		}
		v = n
	}
	if v.IsInt() {
		return v.AsInt(), true
	}
	if v.IsFloat() {
		if ceil {
			return f2iCeil(v.AsFloat())
		}
		return f2iFloor(v.AsFloat())
	}
	return 0, false
}

// forlimit returns the integer loop limit and whether the loop must be skipped
// (lvm.c forlimit).
func (L *LState) forlimit(init int64, lim Value, step int64) (int64, bool) {
	if p, ok := tointegerForLimit(lim, step < 0); ok {
		if step > 0 {
			return p, init > p
		}
		return p, init < p
	}
	flim, ok := tonumberCvt(lim)
	if !ok {
		L.forError("limit", lim)
	}
	if flim > 0 {
		if step < 0 {
			return 0, true
		}
		return math.MaxInt64, init > math.MaxInt64
	}
	if step > 0 {
		return 0, true
	}
	return math.MinInt64, init < math.MinInt64
}

// forprep prepares a numeric for loop at registers ra..ra+3; it returns true if
// the loop body must be skipped entirely (lvm.c forprep).
func (L *LState) forprep(ra int) bool {
	pinit := L.stack[ra]
	plimit := L.stack[ra+1]
	pstep := L.stack[ra+2]
	if pinit.IsInt() && pstep.IsInt() {
		init := pinit.AsInt()
		step := pstep.AsInt()
		if step == 0 {
			L.runtimeError("'for' step is zero")
		}
		L.stack[ra+3] = Int(init)
		limit, skip := L.forlimit(init, plimit, step)
		if skip {
			return true
		}
		var count uint64
		if step > 0 {
			count = uint64(limit) - uint64(init)
			if step != 1 {
				count /= uint64(step)
			}
		} else {
			count = uint64(init) - uint64(limit)
			count /= uint64(-(step+1)) + 1
		}
		L.stack[ra+1] = Int(int64(count)) // counter replaces the limit
		return false
	}
	limit, ok1 := tonumberCvt(plimit)
	if !ok1 {
		L.forError("limit", plimit)
	}
	step, ok2 := tonumberCvt(pstep)
	if !ok2 {
		L.forError("step", pstep)
	}
	init, ok3 := tonumberCvt(pinit)
	if !ok3 {
		L.forError("initial value", pinit)
	}
	if step == 0 {
		L.runtimeError("'for' step is zero")
	}
	if step > 0 {
		if limit < init {
			return true
		}
	} else if init < limit {
		return true
	}
	L.stack[ra+1] = Float(limit)
	L.stack[ra+2] = Float(step)
	L.stack[ra] = Float(init)
	L.stack[ra+3] = Float(init)
	return false
}

// floatforloop advances a float numeric-for loop, returning true to continue
// (lvm.c floatforloop). The integer case is inlined in OP_FORLOOP.
func (L *LState) floatforloop(ra int) bool {
	step := L.stack[ra+2].AsFloat()
	limit := L.stack[ra+1].AsFloat()
	idx := L.stack[ra].AsFloat() + step
	cont := false
	if step > 0 {
		cont = idx <= limit
	} else {
		cont = limit <= idx
	}
	if cont {
		L.stack[ra] = Float(idx)
		L.stack[ra+3] = Float(idx)
		return true
	}
	return false
}

// pushClosure instantiates the nested proto p into stack slot ra, wiring its
// upvalues from the enclosing registers / upvalues (lvm.c pushclosure).
func (L *LState) pushClosure(p *Proto, encup []*Upvalue, base, ra int) {
	ncl := newLuaClosure(p)
	for i := range p.Upvalues {
		ud := p.Upvalues[i]
		if ud.InStack {
			ncl.upvals[i] = L.findupval(base + int(ud.Index))
		} else {
			ncl.upvals[i] = encup[ud.Index]
		}
	}
	L.stack[ra] = mkClosure(ncl)
}

// --- arithmetic helpers (op_arith_aux / op_arithf_aux / op_bitwise) ---

func arithIF(v1, v2 Value, iop func(int64, int64) int64, fop func(float64, float64) float64) (Value, bool) {
	if v1.IsInt() && v2.IsInt() {
		return Int(iop(v1.AsInt(), v2.AsInt())), true
	}
	n1, ok1 := toNumberNS(v1)
	n2, ok2 := toNumberNS(v2)
	if ok1 && ok2 {
		return Float(fop(n1, n2)), true
	}
	return Value{}, false
}

func arithFlt(v1, v2 Value, fop func(float64, float64) float64) (Value, bool) {
	n1, ok1 := toNumberNS(v1)
	n2, ok2 := toNumberNS(v2)
	if ok1 && ok2 {
		return Float(fop(n1, n2)), true
	}
	return Value{}, false
}

func arithBit(v1, v2 Value, bop func(int64, int64) int64) (Value, bool) {
	i1, ok1 := toIntegerNS(v1)
	i2, ok2 := toIntegerNS(v2)
	if ok1 && ok2 {
		return Int(bop(i1, i2)), true
	}
	return Value{}, false
}

// raw arithmetic functions used by the opcodes (llimits.h intop / luai_num*).
func fAdd(a, b float64) float64 { return a + b }
func fSub(a, b float64) float64 { return a - b }
func fMul(a, b float64) float64 { return a * b }
func fDiv(a, b float64) float64 { return a / b }
func fPow(a, b float64) float64 { return math.Pow(a, b) }
func fIDiv(a, b float64) float64 { return math.Floor(a / b) }

func iAdd(a, b int64) int64 { return int64(uint64(a) + uint64(b)) }
func iSub(a, b int64) int64 { return int64(uint64(a) - uint64(b)) }
func iMul(a, b int64) int64 { return int64(uint64(a) * uint64(b)) }
func iBAnd(a, b int64) int64 { return a & b }
func iBOr(a, b int64) int64  { return a | b }
func iBXor(a, b int64) int64 { return a ^ b }
func iShl(a, b int64) int64  { return shiftL(a, b) }
func iShr(a, b int64) int64  { return shiftL(a, -b) }
