package luapure

// Tag-method (metamethod) machinery, ported from ltm.c. Event lookup keys off
// the metamethod field names ("__index", "__add", ...); the TMS enum order
// matches ltm.h so the MMBIN opcodes (whose C field carries a TMS) decode here.

// TMS is a tag-method selector (ltm.h TMS), ORDER TM.
type TMS int

const (
	tmIndex TMS = iota
	tmNewIndex
	tmGC
	tmMode
	tmLen
	tmEq
	tmAdd
	tmSub
	tmMul
	tmMod
	tmPow
	tmDiv
	tmIDiv
	tmBAnd
	tmBOr
	tmBXor
	tmShl
	tmShr
	tmUnm
	tmBNot
	tmLt
	tmLe
	tmConcat
	tmCall
	tmClose
	tmN
)

var tmNames = [tmN]string{
	"__index", "__newindex", "__gc", "__mode", "__len", "__eq",
	"__add", "__sub", "__mul", "__mod", "__pow", "__div", "__idiv",
	"__band", "__bor", "__bxor", "__shl", "__shr",
	"__unm", "__bnot", "__lt", "__le", "__concat", "__call", "__close",
}

func (e TMS) name() string { return tmNames[e] }

// display returns the metamethod name without the "__" prefix, as PUC reports
// it in error messages (getshrstr(tmname) + 2).
func (e TMS) display() string { return tmNames[e][2:] }

// eventForArith maps an arithmetic op code (arith.go OpAdd..OpBNot) to its TMS.
func eventForArith(op int) TMS {
	switch op {
	case OpAdd:
		return tmAdd
	case OpSub:
		return tmSub
	case OpMul:
		return tmMul
	case OpMod:
		return tmMod
	case OpPow:
		return tmPow
	case OpDiv:
		return tmDiv
	case OpIDiv:
		return tmIDiv
	case OpBAnd:
		return tmBAnd
	case OpBOr:
		return tmBOr
	case OpBXor:
		return tmBXor
	case OpShl:
		return tmShl
	case OpShr:
		return tmShr
	case OpUnm:
		return tmUnm
	case OpBNot:
		return tmBNot
	}
	return tmN
}

// metatableOf returns the metatable governing v, or nil (luaT_gettmbyobj's mt
// selection): tables and (full) userdata carry their own; everything else uses
// the per-basic-type table, with strings sharing one.
func (L *LState) metatableOf(v Value) *Table {
	switch v.tag {
	case tagTable:
		return v.tablev().meta
	case tagUserData:
		return v.userData().meta
	case tagString:
		return L.stringMT
	default:
		t := v.Type()
		if t >= 0 && t < numTypeTags {
			return L.basicMT[t]
		}
		return nil
	}
}

// objTypeName is luaT_objtypename: a table or full userdata whose metatable has
// a string __name field reports that name; otherwise the basic type name.
func (L *LState) objTypeName(v Value) string {
	if v.IsTable() || v.IsUserData() {
		if mt := L.metatableOf(v); mt != nil {
			if name := mt.rawgetStr("__name"); name.IsString() {
				return name.Str()
			}
		}
	}
	return typeName(v)
}

// gettmByObj returns the event metamethod for v, or Nil (luaT_gettmbyobj).
func (L *LState) gettmByObj(v Value, event TMS) Value {
	mt := L.metatableOf(v)
	if mt == nil {
		return Nil
	}
	return mt.rawgetStr(event.name())
}

// scratchTop returns a stack index safe to use for temporary values pushed by a
// metamethod call: above the current frame's registers (ci.top) and above any
// values already pushed (L.top). This is the index analogue of PUC's Protect
// macro resetting L->top to ci->top before a tag-method call.
func (L *LState) scratchTop() int {
	t := L.top
	if L.ci != nil && L.ci.top > t {
		t = L.ci.top
	}
	return t
}

// callTMres calls metamethod f with (p1, p2) and stores its single result at
// stack index res (luaT_callTMres).
func (L *LState) callTMres(f, p1, p2 Value, res int) {
	funcIdx := L.scratchTop()
	L.top = funcIdx
	L.push(f)
	L.push(p1)
	L.push(p2)
	L.call(funcIdx, 1)
	L.top--
	L.stack[res] = L.stack[L.top]
}

// callTM calls metamethod f with (p1, p2, p3) and no results (luaT_callTM).
func (L *LState) callTM(f, p1, p2, p3 Value) {
	funcIdx := L.scratchTop()
	L.top = funcIdx
	L.push(f)
	L.push(p1)
	L.push(p2)
	L.push(p3)
	L.call(funcIdx, 0)
}

// callbinTM tries the event metamethod of p1 then p2, calling it into res.
func (L *LState) callbinTM(p1, p2 Value, res int, event TMS) bool {
	tm := L.gettmByObj(p1, event)
	if tm.IsNil() {
		tm = L.gettmByObj(p2, event)
	}
	if tm.IsNil() {
		return false
	}
	L.callTMres(tm, p1, p2, res)
	return true
}

// trybinTM applies a binary metamethod, raising the appropriate error if none
// exists (luaT_trybinTM).
func (L *LState) trybinTM(p1, p2 Value, res int, event TMS) {
	if L.callbinTM(p1, p2, res, event) {
		return
	}
	switch event {
	case tmBAnd, tmBOr, tmBXor, tmShl, tmShr, tmBNot:
		if p1.IsNumber() && p2.IsNumber() {
			L.tointError()
		}
		L.opInterError(p1, p2, "perform bitwise operation on")
	default:
		L.opInterError(p1, p2, "perform arithmetic on")
	}
}

// tointError raises luaG_tointerror's "number%s has no integer representation",
// appending name-info for the offending operand register (set by the opcode).
func (L *LState) tointError() {
	info := L.nameInfo(L.errReg)
	if info == "" {
		info = L.upvalNameInfo(L.errUpval)
	}
	L.errReg = -1
	L.errUpval = -1
	L.runtimeError("number%s has no integer representation", info)
}

// isBitwiseTM reports whether event is one of the bitwise metamethods.
func isBitwiseTM(event TMS) bool {
	switch event {
	case tmBAnd, tmBOr, tmBXor, tmShl, tmShr, tmBNot:
		return true
	}
	return false
}

// arithBadReg returns the register of the operand an arithmetic/bitwise error
// should name, mirroring luaG_opinterror/luaG_tointerror's p1-first selection.
// regB == -1 means the second operand is not in a register (immediate/constant).
func arithBadReg(p1, p2 Value, regA, regB int, bitwise bool) int {
	if bitwise && p1.IsNumber() && p2.IsNumber() {
		if _, ok := toIntegerNS(p1); !ok {
			return regA
		}
		return regB
	}
	if !p1.IsNumber() {
		return regA
	}
	return regB
}

// opInterError raises an error naming the non-numeric operand (luaG_opinterror).
func (L *LState) opInterError(p1, p2 Value, verb string) {
	// luaG_opinterror: the culprit is the first operand that is not a number
	// (strict ttisnumber — a numeric string does NOT count).
	bad := p2
	if !p1.IsNumber() {
		bad = p1
	}
	L.typeError(bad, verb)
}

func (L *LState) trybinassocTM(p1, p2 Value, flip bool, res int, event TMS) {
	if flip {
		L.trybinTM(p2, p1, res, event)
	} else {
		L.trybinTM(p1, p2, res, event)
	}
}

func (L *LState) trybiniTM(p1 Value, i2 int64, flip bool, res int, event TMS) {
	L.trybinassocTM(p1, Int(i2), flip, res, event)
}

// tryconcatTM handles a __concat fallback for the top two stack values
// (luaT_tryconcatTM).
func (L *LState) tryconcatTM() {
	top := L.top
	if !L.callbinTM(L.stack[top-2], L.stack[top-1], top-2, tmConcat) {
		L.concatError(L.stack[top-2], L.stack[top-1])
	}
}

func (L *LState) concatError(p1, p2 Value) {
	bad := p1
	if p1.IsString() || p1.IsNumber() {
		bad = p2
	}
	L.typeError(bad, "concatenate")
}

// callorderTM calls an ordering metamethod, returning its boolean result, or
// raising "attempt to compare" if none exists (luaT_callorderTM).
func (L *LState) callorderTM(p1, p2 Value, event TMS) bool {
	res := L.scratchTop()
	if L.callbinTM(p1, p2, res, event) {
		return !L.stack[res].IsFalsy()
	}
	// LUA_COMPAT_LT_LE (default-on in stock 5.4's luaconf.h): with no '__le',
	// emulate 'p1 <= p2' as '!(p2 < p1)' through '__lt'.
	if event == tmLe && L.callbinTM(p2, p1, res, tmLt) {
		return L.stack[res].IsFalsy()
	}
	L.orderError(p1, p2)
	return false
}

func (L *LState) callorderiTM(p1 Value, v2 int, flip, isfloat bool, event TMS) bool {
	var aux Value
	if isfloat {
		aux = Float(float64(v2))
	} else {
		aux = Int(int64(v2))
	}
	if flip {
		return L.callorderTM(aux, p1, event)
	}
	return L.callorderTM(p1, aux, event)
}

func (L *LState) orderError(p1, p2 Value) {
	t1, t2 := L.objTypeName(p1), L.objTypeName(p2)
	if t1 == t2 {
		L.runtimeError("attempt to compare two %s values", t1)
	}
	L.runtimeError("attempt to compare %s with %s", t1, t2)
}

// --- varargs (ltm.c luaT_adjustvarargs / luaT_getvarargs) ---

// adjustvarargs moves the extra (beyond fixed) arguments out of the way so the
// fixed parameters sit at the new frame base, recording the extra count.
func (L *LState) adjustvarargs(nfix int, ci *callInfo, p *Proto) {
	actual := L.top - ci.fn - 1 // number of arguments passed
	nextra := actual - nfix
	if nextra < 0 {
		nextra = 0
	}
	ci.nextra = nextra
	L.checkstack(int(p.MaxStackSize) + 1)
	// Copy the function to the new top, then the fixed params, niling originals.
	L.stack[L.top] = L.stack[ci.fn]
	L.top++
	for i := 1; i <= nfix; i++ {
		L.stack[L.top] = L.stack[ci.fn+i]
		L.top++
		L.stack[ci.fn+i] = Nil
	}
	ci.fn += actual + 1
	ci.base = ci.fn + 1
	ci.top += actual + 1
}

// getvarargs copies the frame's extra arguments to `where` (luaT_getvarargs).
// wanted < 0 means "all available".
func (L *LState) getvarargs(ci *callInfo, where, wanted int) {
	nextra := ci.nextra
	if wanted < 0 {
		wanted = nextra
		L.checkstack(nextra)
		L.top = where + nextra
	}
	i := 0
	for ; i < wanted && i < nextra; i++ {
		L.stack[where+i] = L.stack[ci.fn-nextra+i]
	}
	for ; i < wanted; i++ {
		L.stack[where+i] = Nil
	}
}
