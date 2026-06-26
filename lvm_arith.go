package luapure

import "math"

// Raw arithmetic on Values, ported from PUC-Lua 5.4.8 (lobject.c luaO_rawarith,
// intarith/numarith; lvm.c luaV_idiv/mod/modf/shiftl; llimits.h intop/luai_num*).
// This is the metamethod-free numeric core: it is used by the compiler for
// constant folding (lcode.go constfolding) and will be reused by the VM for the
// arithmetic opcodes. "Raw" means it never invokes metamethods — it returns
// ok=false when the operands are not numbers (or not integer-convertible for
// bitwise ops), leaving the metamethod path to the caller.

// Arithmetic operation codes, matching lua.h LUA_OP* (ORDER OP). The values are
// significant: BinOpr/UnOpr map onto this range by a fixed offset (see lcode.go
// binopr2op / constfolding).
const (
	OpAdd  = iota // +
	OpSub         // -
	OpMul         // *
	OpMod         // %
	OpPow         // ^
	OpDiv         // /
	OpIDiv        // //
	OpBAnd        // &
	OpBOr         // |
	OpBXor        // ~ (binary)
	OpShl         // <<
	OpShr         // >>
	OpUnm         // unary -
	OpBNot        // ~ (unary)
)

// numberToInteger converts a float that is already known to be integral into an
// int64, failing if it is out of int64 range (PUC lua_numbertointeger).
func numberToInteger(f float64) (int64, bool) {
	// float64(math.MinInt64) is exactly -2^63; its negation is exactly 2^63.
	const minIntF = float64(math.MinInt64)
	if f >= minIntF && f < -minIntF {
		return int64(f), true
	}
	return 0, false
}

// fltToIntEq converts a float to an integer only when it has an exact integral
// value (PUC luaV_flttointeger with mode F2Ieq).
func fltToIntEq(n float64) (int64, bool) {
	f := math.Floor(n)
	if n != f { // not an integral value
		return 0, false
	}
	return numberToInteger(f)
}

// toIntegerNS converts a number Value to an integer without metamethods
// (PUC tointegerns / luaV_tointegerns, mode F2Ieq). Non-numbers fail.
func toIntegerNS(v Value) (int64, bool) {
	switch {
	case v.IsInt():
		return v.AsInt(), true
	case v.IsFloat():
		return fltToIntEq(v.AsFloat())
	default:
		return 0, false
	}
}

// toNumberNS converts a number Value to a float without metamethods
// (PUC tonumberns). Non-numbers fail.
func toNumberNS(v Value) (float64, bool) {
	switch {
	case v.IsFloat():
		return v.AsFloat(), true
	case v.IsInt():
		return float64(v.AsInt()), true
	default:
		return 0, false
	}
}

// intArith applies an integer arithmetic op (PUC intarith). Division and modulo
// callers must ensure the divisor is non-zero (validop guarantees this during
// folding); the -1 overflow corner is handled here.
func intArith(op int, v1, v2 int64) int64 {
	switch op {
	case OpAdd:
		return int64(uint64(v1) + uint64(v2))
	case OpSub:
		return int64(uint64(v1) - uint64(v2))
	case OpMul:
		return int64(uint64(v1) * uint64(v2))
	case OpMod:
		return intMod(v1, v2)
	case OpIDiv:
		return intIDiv(v1, v2)
	case OpBAnd:
		return v1 & v2
	case OpBOr:
		return v1 | v2
	case OpBXor:
		return v1 ^ v2
	case OpShl:
		return shiftL(v1, v2)
	case OpShr:
		return shiftL(v1, -v2)
	case OpUnm:
		return int64(uint64(0) - uint64(v1))
	case OpBNot:
		return ^int64(0) ^ v1
	}
	return 0
}

// numArith applies a float arithmetic op (PUC numarith).
func numArith(op int, v1, v2 float64) float64 {
	switch op {
	case OpAdd:
		return v1 + v2
	case OpSub:
		return v1 - v2
	case OpMul:
		return v1 * v2
	case OpDiv:
		return v1 / v2
	case OpPow:
		return math.Pow(v1, v2)
	case OpIDiv:
		return math.Floor(v1 / v2)
	case OpUnm:
		return -v1
	case OpMod:
		return numMod(v1, v2)
	}
	return 0
}

// intMod is integer modulo with Lua's floored-division semantics (PUC luaV_mod).
func intMod(m, n int64) int64 {
	if n == -1 { // avoid overflow with MININT % -1
		return 0
	}
	r := m % n
	if r != 0 && (r^n) < 0 { // result would be a non-integer negative
		r += n
	}
	return r
}

// intIDiv is integer floor division (PUC luaV_idiv).
func intIDiv(m, n int64) int64 {
	if n == -1 { // avoid overflow with MININT // -1
		return int64(uint64(0) - uint64(m))
	}
	q := m / n
	if (m^n) < 0 && m%n != 0 { // floor for different-sign non-exact quotient
		q--
	}
	return q
}

// numMod is float modulo with Lua's floored semantics (PUC luai_nummod).
func numMod(a, b float64) float64 {
	m := math.Mod(a, b)
	if m > 0 {
		if b < 0 {
			m += b
		}
	} else if m < 0 && b > 0 {
		m += b
	}
	return m
}

// shiftL shifts x left by y bits, where a negative y shifts right; shifts of
// NBITS or more produce 0 (PUC luaV_shiftl). Shifts use logical (unsigned) bits.
func shiftL(x, y int64) int64 {
	const nbits = 64
	if y < 0 { // shift right
		if y <= -nbits {
			return 0
		}
		return int64(uint64(x) >> uint(-y))
	}
	if y >= nbits { // shift left
		return 0
	}
	return int64(uint64(x) << uint(y))
}

// rawArith performs op on two number Values without metamethods, returning
// ok=false when an operand is non-numeric (or non-integer-convertible for a
// bitwise op). Ported from PUC luaO_rawarith.
func rawArith(op int, p1, p2 Value) (Value, bool) {
	switch op {
	case OpBAnd, OpBOr, OpBXor, OpShl, OpShr, OpBNot: // integers only
		i1, ok1 := toIntegerNS(p1)
		i2, ok2 := toIntegerNS(p2)
		if ok1 && ok2 {
			return Int(intArith(op, i1, i2)), true
		}
		return Value{}, false
	case OpDiv, OpPow: // floats only
		n1, ok1 := toNumberNS(p1)
		n2, ok2 := toNumberNS(p2)
		if ok1 && ok2 {
			return Float(numArith(op, n1, n2)), true
		}
		return Value{}, false
	default: // add/sub/mul/mod/idiv/unm: integer if both are integers
		if p1.IsInt() && p2.IsInt() {
			return Int(intArith(op, p1.AsInt(), p2.AsInt())), true
		}
		n1, ok1 := toNumberNS(p1)
		n2, ok2 := toNumberNS(p2)
		if ok1 && ok2 {
			return Float(numArith(op, n1, n2)), true
		}
		return Value{}, false
	}
}
