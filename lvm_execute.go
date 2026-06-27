package luapure

// execute runs Lua frames starting at ci until that fresh frame returns
// (lvm.c luaV_execute). The outer loop is the "startfunc"/"returning" entry;
// the inner loop is the instruction dispatch. There is no hook/trap handling
// yet, so the two entry points coincide.
func (L *LState) execute(ci *callInfo) {
frame:
	for {
		cl := L.stack[ci.fn].closure()
		proto := cl.proto
		code := proto.Code
		k := proto.Constants
		base := ci.base
		pc := ci.savedpc

		for {
			i := code[pc]
			pc++
			ci.savedpc = pc // keep savedpc current for errors / metamethod calls
			if L.hookMask != 0 {
				L.traceexec(ci, proto, pc)
				base = ci.base
			}
			// Poll for __gc finalizers between instructions (cheap counter; the
			// atomic / forced GC only happen every finGCPoll'th instruction, and
			// only while finalizers are outstanding). This is what lets a loop
			// spinning on a finalizer side effect — gc.lua's `repeat u={} until
			// finish` — make progress without calling any function.
			if L.finGCTick++; L.finGCTick >= finGCPoll {
				L.finGCTick = 0
				if L.pollFinalizers() {
					base = ci.base
				}
				// Cooperative cancellation: reuse the same gate so the per-
				// instruction cost stays a bare counter increment. A cancelled
				// context raises a catchable error caught by the enclosing
				// protected call (Call/DoString/RunWith).
				if L.ctx != nil {
					if err := L.ctx.Err(); err != nil {
						L.throw(MkString("execution cancelled: " + err.Error()))
					}
				}
			}
			L.errReg = -1 // operand register for name-aware type errors
			L.errUpval = -1
			a := GetArgA(i)
			ra := base + a

			switch GetOpCode(i) {
			case OP_MOVE:
				L.stack[ra] = L.stack[base+GetArgB(i)]
			case OP_LOADI:
				L.stack[ra] = Int(int64(GetArgsBx(i)))
			case OP_LOADF:
				L.stack[ra] = Float(float64(GetArgsBx(i)))
			case OP_LOADK:
				L.stack[ra] = k[GetArgBx(i)]
			case OP_LOADKX:
				L.stack[ra] = k[GetArgAx(code[pc])]
				pc++
			case OP_LOADFALSE:
				L.stack[ra] = False
			case OP_LFALSESKIP:
				L.stack[ra] = False
				pc++
			case OP_LOADTRUE:
				L.stack[ra] = True
			case OP_LOADNIL:
				for j := 0; j <= GetArgB(i); j++ {
					L.stack[ra+j] = Nil
				}
			case OP_GETUPVAL:
				L.stack[ra] = cl.upvals[GetArgB(i)].get()
			case OP_SETUPVAL:
				cl.upvals[GetArgB(i)].set(L.stack[ra])

			case OP_GETTABUP:
				L.errUpval = GetArgB(i)
				up := cl.upvals[GetArgB(i)].get()
				L.gettable(up, k[GetArgC(i)], ra)
			case OP_GETTABLE:
				L.errReg = GetArgB(i)
				L.gettable(L.stack[base+GetArgB(i)], L.stack[base+GetArgC(i)], ra)
			case OP_GETI:
				L.errReg = GetArgB(i)
				L.gettable(L.stack[base+GetArgB(i)], Int(int64(GetArgC(i))), ra)
			case OP_GETFIELD:
				L.errReg = GetArgB(i)
				L.gettable(L.stack[base+GetArgB(i)], k[GetArgC(i)], ra)

			case OP_SETTABUP:
				L.errUpval = GetArgA(i)
				up := cl.upvals[GetArgA(i)].get()
				L.settable(up, k[GetArgB(i)], rkc(L, i, base, k))
			case OP_SETTABLE:
				L.errReg = a
				L.settable(L.stack[ra], L.stack[base+GetArgB(i)], rkc(L, i, base, k))
			case OP_SETI:
				L.errReg = a
				L.settable(L.stack[ra], Int(int64(GetArgB(i))), rkc(L, i, base, k))
			case OP_SETFIELD:
				L.errReg = a
				L.settable(L.stack[ra], k[GetArgB(i)], rkc(L, i, base, k))

			case OP_NEWTABLE:
				c := GetArgC(i)
				if Testk(i) {
					c += GetArgAx(code[pc]) * (MaxArgC + 1)
				}
				pc++ // skip the EXTRAARG
				t := newTable()
				if c > 0 {
					t.arr = make([]Value, 0, c)
				}
				L.stack[ra] = mkTable(t)
			case OP_SELF:
				rb := L.stack[base+GetArgB(i)]
				L.stack[ra+1] = rb
				L.errReg = GetArgB(i)
				L.gettable(rb, rkc(L, i, base, k), ra)

			// --- arithmetic with immediate / register / constant operands ---
			case OP_ADDI:
				if arithImm(L, ra, L.stack[base+GetArgB(i)], GetArgsC(i), iAdd, fAdd) {
					pc++
				}
			case OP_ADDK:
				if v, ok := arithIF(L.stack[base+GetArgB(i)], k[GetArgC(i)], iAdd, fAdd); ok {
					L.stack[ra] = v
					pc++
				}
			case OP_SUBK:
				if v, ok := arithIF(L.stack[base+GetArgB(i)], k[GetArgC(i)], iSub, fSub); ok {
					L.stack[ra] = v
					pc++
				}
			case OP_MULK:
				if v, ok := arithIF(L.stack[base+GetArgB(i)], k[GetArgC(i)], iMul, fMul); ok {
					L.stack[ra] = v
					pc++
				}
			case OP_MODK:
				if L.arithModK(ra, L.stack[base+GetArgB(i)], k[GetArgC(i)]) {
					pc++
				}
			case OP_POWK:
				if v, ok := arithFlt(L.stack[base+GetArgB(i)], k[GetArgC(i)], fPow); ok {
					L.stack[ra] = v
					pc++
				}
			case OP_DIVK:
				if v, ok := arithFlt(L.stack[base+GetArgB(i)], k[GetArgC(i)], fDiv); ok {
					L.stack[ra] = v
					pc++
				}
			case OP_IDIVK:
				if L.arithIDivK(ra, L.stack[base+GetArgB(i)], k[GetArgC(i)]) {
					pc++
				}
			case OP_BANDK:
				if v, ok := arithBit(L.stack[base+GetArgB(i)], k[GetArgC(i)], iBAnd); ok {
					L.stack[ra] = v
					pc++
				}
			case OP_BORK:
				if v, ok := arithBit(L.stack[base+GetArgB(i)], k[GetArgC(i)], iBOr); ok {
					L.stack[ra] = v
					pc++
				}
			case OP_BXORK:
				if v, ok := arithBit(L.stack[base+GetArgB(i)], k[GetArgC(i)], iBXor); ok {
					L.stack[ra] = v
					pc++
				}
			case OP_SHRI:
				if ib, ok := toIntegerNS(L.stack[base+GetArgB(i)]); ok {
					L.stack[ra] = Int(shiftL(ib, int64(-GetArgsC(i))))
					pc++
				}
			case OP_SHLI:
				if ib, ok := toIntegerNS(L.stack[base+GetArgB(i)]); ok {
					L.stack[ra] = Int(shiftL(int64(GetArgsC(i)), ib))
					pc++
				}
			case OP_ADD:
				if v, ok := arithIF(L.stack[base+GetArgB(i)], L.stack[base+GetArgC(i)], iAdd, fAdd); ok {
					L.stack[ra] = v
					pc++
				}
			case OP_SUB:
				if v, ok := arithIF(L.stack[base+GetArgB(i)], L.stack[base+GetArgC(i)], iSub, fSub); ok {
					L.stack[ra] = v
					pc++
				}
			case OP_MUL:
				if v, ok := arithIF(L.stack[base+GetArgB(i)], L.stack[base+GetArgC(i)], iMul, fMul); ok {
					L.stack[ra] = v
					pc++
				}
			case OP_MOD:
				if L.arithMod(ra, L.stack[base+GetArgB(i)], L.stack[base+GetArgC(i)]) {
					pc++
				}
			case OP_POW:
				if v, ok := arithFlt(L.stack[base+GetArgB(i)], L.stack[base+GetArgC(i)], fPow); ok {
					L.stack[ra] = v
					pc++
				}
			case OP_DIV:
				if v, ok := arithFlt(L.stack[base+GetArgB(i)], L.stack[base+GetArgC(i)], fDiv); ok {
					L.stack[ra] = v
					pc++
				}
			case OP_IDIV:
				if L.arithIDiv(ra, L.stack[base+GetArgB(i)], L.stack[base+GetArgC(i)]) {
					pc++
				}
			case OP_BAND:
				if v, ok := arithBit(L.stack[base+GetArgB(i)], L.stack[base+GetArgC(i)], iBAnd); ok {
					L.stack[ra] = v
					pc++
				}
			case OP_BOR:
				if v, ok := arithBit(L.stack[base+GetArgB(i)], L.stack[base+GetArgC(i)], iBOr); ok {
					L.stack[ra] = v
					pc++
				}
			case OP_BXOR:
				if v, ok := arithBit(L.stack[base+GetArgB(i)], L.stack[base+GetArgC(i)], iBXor); ok {
					L.stack[ra] = v
					pc++
				}
			case OP_SHL:
				if v, ok := arithBit(L.stack[base+GetArgB(i)], L.stack[base+GetArgC(i)], iShl); ok {
					L.stack[ra] = v
					pc++
				}
			case OP_SHR:
				if v, ok := arithBit(L.stack[base+GetArgB(i)], L.stack[base+GetArgC(i)], iShr); ok {
					L.stack[ra] = v
					pc++
				}

			// --- metamethod follow-ups for the arithmetic opcodes ---
			case OP_MMBIN:
				pi := code[pc-2]
				result := base + GetArgA(pi)
				ev := TMS(GetArgC(i))
				L.errReg = arithBadReg(L.stack[ra], L.stack[base+GetArgB(i)], a, GetArgB(i), isBitwiseTM(ev))
				L.trybinTM(L.stack[ra], L.stack[base+GetArgB(i)], result, ev)
			case OP_MMBINI:
				pi := code[pc-2]
				result := base + GetArgA(pi)
				ev := TMS(GetArgC(i))
				L.errReg = arithBadReg(L.stack[ra], Int(int64(GetArgsB(i))), a, -1, isBitwiseTM(ev))
				L.trybiniTM(L.stack[ra], int64(GetArgsB(i)), GetArgk(i) != 0, result, ev)
			case OP_MMBINK:
				pi := code[pc-2]
				result := base + GetArgA(pi)
				ev := TMS(GetArgC(i))
				L.errReg = arithBadReg(L.stack[ra], k[GetArgB(i)], a, -1, isBitwiseTM(ev))
				L.trybinassocTM(L.stack[ra], k[GetArgB(i)], GetArgk(i) != 0, result, ev)

			case OP_UNM:
				rb := L.stack[base+GetArgB(i)]
				if rb.IsInt() {
					L.stack[ra] = Int(iSub(0, rb.AsInt()))
				} else if n, ok := toNumberNS(rb); ok {
					L.stack[ra] = Float(-n)
				} else {
					L.errReg = GetArgB(i)
					L.trybinTM(rb, rb, ra, tmUnm)
				}
			case OP_BNOT:
				rb := L.stack[base+GetArgB(i)]
				if iv, ok := toIntegerNS(rb); ok {
					L.stack[ra] = Int(^iv)
				} else {
					L.errReg = GetArgB(i)
					L.trybinTM(rb, rb, ra, tmBNot)
				}
			case OP_NOT:
				L.stack[ra] = Bool(L.stack[base+GetArgB(i)].IsFalsy())
			case OP_LEN:
				L.errReg = GetArgB(i)
				L.objlen(ra, L.stack[base+GetArgB(i)])

			case OP_CONCAT:
				n := GetArgB(i)
				L.top = ra + n
				L.concat(n)

			case OP_CLOSE:
				L.closeAll(ra, Nil)
			case OP_TBC:
				L.newtbcupval(ra)

			case OP_JMP:
				pc += GetArgsJ(i)

			case OP_EQ:
				cond := L.equalobj(L.stack[ra], L.stack[base+GetArgB(i)])
				pc = condJump(cond, i, pc, code)
			case OP_LT:
				sa := L.stack[ra]
				rb := L.stack[base+GetArgB(i)]
				var cond bool
				if sa.IsNumber() && rb.IsNumber() {
					cond = numLT(sa, rb)
				} else {
					cond = L.lessthan(sa, rb)
				}
				pc = condJump(cond, i, pc, code)
			case OP_LE:
				sa := L.stack[ra]
				rb := L.stack[base+GetArgB(i)]
				var cond bool
				if sa.IsNumber() && rb.IsNumber() {
					cond = numLE(sa, rb)
				} else {
					cond = L.lessequal(sa, rb)
				}
				pc = condJump(cond, i, pc, code)
			case OP_EQK:
				cond := L.stack[ra].RawEqual(k[GetArgB(i)])
				pc = condJump(cond, i, pc, code)
			case OP_EQI:
				sa := L.stack[ra]
				im := GetArgsB(i)
				var cond bool
				if sa.IsInt() {
					cond = sa.AsInt() == int64(im)
				} else if sa.IsFloat() {
					cond = sa.AsFloat() == float64(im)
				}
				pc = condJump(cond, i, pc, code)
			case OP_LTI:
				pc = condJump(L.orderI(i, base, opLT), i, pc, code)
			case OP_LEI:
				pc = condJump(L.orderI(i, base, opLE), i, pc, code)
			case OP_GTI:
				pc = condJump(L.orderI(i, base, opGT), i, pc, code)
			case OP_GEI:
				pc = condJump(L.orderI(i, base, opGE), i, pc, code)

			case OP_TEST:
				cond := !L.stack[ra].IsFalsy()
				pc = condJump(cond, i, pc, code)
			case OP_TESTSET:
				rb := L.stack[base+GetArgB(i)]
				falsy := 0
				if rb.IsFalsy() {
					falsy = 1
				}
				if falsy == GetArgk(i) {
					pc++
				} else {
					L.stack[ra] = rb
					ni := code[pc]
					pc += GetArgsJ(ni) + 1
				}

			case OP_CALL:
				b := GetArgB(i)
				if b != 0 {
					L.top = ra + b
				}
				L.errReg = a
				newci := L.precall(ra, GetArgC(i)-1)
				if newci != nil {
					ci = newci
					continue frame
				}
			case OP_TAILCALL:
				b := GetArgB(i)
				nparams1 := GetArgC(i)
				delta := 0
				if nparams1 != 0 {
					delta = ci.nextra + nparams1
				}
				if b != 0 {
					L.top = ra + b
				} else {
					b = L.top - ra
				}
				if Testk(i) {
					L.closeUpvals(base)
				}
				L.errReg = a
				n := L.pretailcall(ci, ra, b, delta)
				if n < 0 {
					continue frame // Lua callee: frame reused
				}
				ci.fn -= delta
				L.poscall(ci, n)
				if ci.status&cistFresh != 0 {
					return
				}
				ci = ci.prev
				continue frame

			case OP_RETURN:
				n := GetArgB(i) - 1
				nparams1 := GetArgC(i)
				if n < 0 {
					n = L.top - ra
				}
				if Testk(i) {
					ci.nres = n
					if L.top < ci.top {
						L.top = ci.top
					}
					L.closeAll(base, Nil)
				}
				if nparams1 != 0 {
					ci.fn -= ci.nextra + nparams1
				}
				L.top = ra + n
				L.poscall(ci, n)
				if ci.status&cistFresh != 0 {
					return
				}
				ci = ci.prev
				continue frame
			case OP_RETURN0:
				L.top = ra
				L.poscall(ci, 0)
				if ci.status&cistFresh != 0 {
					return
				}
				ci = ci.prev
				continue frame
			case OP_RETURN1:
				L.top = ra + 1
				L.poscall(ci, 1)
				if ci.status&cistFresh != 0 {
					return
				}
				ci = ci.prev
				continue frame

			case OP_FORLOOP:
				if L.stack[ra+2].IsInt() {
					count := uint64(L.stack[ra+1].AsInt())
					if count > 0 {
						step := L.stack[ra+2].AsInt()
						idx := L.stack[ra].AsInt()
						L.stack[ra+1] = Int(int64(count - 1))
						idx = iAdd(idx, step)
						L.stack[ra] = Int(idx)
						L.stack[ra+3] = Int(idx)
						pc -= GetArgBx(i)
					}
				} else if L.floatforloop(ra) {
					pc -= GetArgBx(i)
				}
			case OP_FORPREP:
				if L.forprep(ra) {
					pc += GetArgBx(i) + 1
				}

			case OP_TFORPREP:
				L.newtbcupval(ra + 3)
				pc += GetArgBx(i)
			case OP_TFORCALL:
				L.stack[ra+4] = L.stack[ra]
				L.stack[ra+5] = L.stack[ra+1]
				L.stack[ra+6] = L.stack[ra+2]
				L.top = ra + 4 + 3
				// Run the iterator in this flat loop (like OP_CALL) rather than a
				// Go-recursive L.call, so a yield from the iterator unwinds cleanly
				// to resume. A Lua iterator reuses the dispatch loop and returns to
				// the following TFORLOOP; a native iterator ran in precall, so we
				// fall through to TFORLOOP with its results already at ra+4.
				newci := L.precall(ra+4, GetArgC(i))
				if newci != nil {
					ci = newci
					continue frame
				}
			case OP_TFORLOOP:
				if !L.stack[ra+4].IsNil() {
					L.stack[ra+2] = L.stack[ra+4]
					pc -= GetArgBx(i)
				}

			case OP_SETLIST:
				n := GetArgB(i)
				base := GetArgC(i)
				h := L.stack[ra].tablev()
				if n == 0 {
					n = L.top - ra - 1
				} else {
					L.top = ci.top
				}
				if Testk(i) {
					base += GetArgAx(code[pc]) * (MaxArgC + 1)
					pc++
				}
				// Fill indices base+1..base+n in ascending order so each lands on
				// the array fast path (k == len(arr)+1 → append). Filling high
				// index first (as the constructor emits them) would route every
				// element but the first through the hash part and then re-absorb
				// them, the dominant alloc-churn source for table constructors.
				for j := 1; j <= n; j++ {
					h.rawsetInt(int64(base+j), L.stack[ra+j])
				}

			case OP_CLOSURE:
				L.pushClosure(proto.Protos[GetArgBx(i)], cl.upvals, base, ra)

			case OP_VARARG:
				L.getvarargs(ci, ra, GetArgC(i)-1)
			case OP_VARARGPREP:
				L.adjustvarargs(GetArgA(i), ci, proto)
				base = ci.base // function has a new base after adjustment

			case OP_EXTRAARG:
				// only ever consumed inline by the preceding instruction
			}
		}
	}
}

// rkc decodes the C operand as a constant (k flag set) or a register value.
func rkc(L *LState, i Instruction, base int, k []Value) Value {
	if Testk(i) {
		return k[GetArgC(i)]
	}
	return L.stack[base+GetArgC(i)]
}

// condJump implements docondjump: take the following JMP when cond matches the
// instruction's k flag, otherwise skip it.
func condJump(cond bool, i Instruction, pc int, code []Instruction) int {
	ci := 0
	if cond {
		ci = 1
	}
	if ci != GetArgk(i) {
		return pc + 1 // skip the jump
	}
	ni := code[pc]
	return pc + GetArgsJ(ni) + 1 // donextjump
}

// immediate-order comparison kinds for OP_LTI/LEI/GTI/GEI.
type orderKind int

const (
	opLT orderKind = iota
	opLE
	opGT
	opGE
)

// orderI evaluates an order comparison against an immediate (op_orderI).
func (L *LState) orderI(i Instruction, base int, kind orderKind) bool {
	sa := L.stack[base+GetArgA(i)]
	im := GetArgsB(i)
	if sa.IsInt() {
		ia := sa.AsInt()
		switch kind {
		case opLT:
			return ia < int64(im)
		case opLE:
			return ia <= int64(im)
		case opGT:
			return ia > int64(im)
		default:
			return ia >= int64(im)
		}
	}
	if sa.IsFloat() {
		fa := sa.AsFloat()
		switch kind {
		case opLT:
			return fa < float64(im)
		case opLE:
			return fa <= float64(im)
		case opGT:
			return fa > float64(im)
		default:
			return fa >= float64(im)
		}
	}
	// non-number: use the order metamethod (flip for GT/GE), per op_orderI.
	isf := GetArgC(i) != 0
	switch kind {
	case opLT:
		return L.callorderiTM(sa, im, false, isf, tmLt)
	case opLE:
		return L.callorderiTM(sa, im, false, isf, tmLe)
	case opGT:
		return L.callorderiTM(sa, im, true, isf, tmLt)
	default:
		return L.callorderiTM(sa, im, true, isf, tmLe)
	}
}

// --- mod / idiv with divide-by-zero checks (op_arith with savestate) ---

func (L *LState) arithMod(ra int, v1, v2 Value) bool {
	if v1.IsInt() && v2.IsInt() {
		if v2.AsInt() == 0 {
			L.runtimeError("attempt to perform 'n%%0'")
		}
		L.stack[ra] = Int(intMod(v1.AsInt(), v2.AsInt()))
		return true
	}
	return L.arithModFloat(ra, v1, v2)
}

func (L *LState) arithModK(ra int, v1, v2 Value) bool { return L.arithMod(ra, v1, v2) }

func (L *LState) arithModFloat(ra int, v1, v2 Value) bool {
	n1, ok1 := toNumberNS(v1)
	n2, ok2 := toNumberNS(v2)
	if ok1 && ok2 {
		L.stack[ra] = Float(numMod(n1, n2))
		return true
	}
	return false
}

func (L *LState) arithIDiv(ra int, v1, v2 Value) bool {
	if v1.IsInt() && v2.IsInt() {
		if v2.AsInt() == 0 {
			L.runtimeError("attempt to divide by zero")
		}
		L.stack[ra] = Int(intIDiv(v1.AsInt(), v2.AsInt()))
		return true
	}
	n1, ok1 := toNumberNS(v1)
	n2, ok2 := toNumberNS(v2)
	if ok1 && ok2 {
		L.stack[ra] = Float(fIDiv(n1, n2))
		return true
	}
	return false
}

func (L *LState) arithIDivK(ra int, v1, v2 Value) bool { return L.arithIDiv(ra, v1, v2) }

// arithImm handles an immediate arithmetic op (op_arithI): register op small int.
func arithImm(L *LState, ra int, v1 Value, imm int, iop func(int64, int64) int64, fop func(float64, float64) float64) bool {
	if v1.IsInt() {
		L.stack[ra] = Int(iop(v1.AsInt(), int64(imm)))
		return true
	}
	if v1.IsFloat() {
		L.stack[ra] = Float(fop(v1.AsFloat(), float64(imm)))
		return true
	}
	return false
}
