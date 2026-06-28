package luapure

import (
	"math"
	"sync/atomic"
	"time"
)

// The math library (lmathlib.c).

func (L *LState) OpenMath() {
	m := newTable()
	setFuncs(m, map[string]GoFunc{
		"abs":        mathAbs,
		"ceil":       mathCeil,
		"floor":      mathFloor,
		"sqrt":       math1(math.Sqrt),
		"sin":        math1(math.Sin),
		"cos":        math1(math.Cos),
		"tan":        math1(math.Tan),
		"asin":       math1(math.Asin),
		"acos":       math1(math.Acos),
		"atan":       mathAtan,
		"exp":        math1(math.Exp),
		"deg":        mathDeg,
		"rad":        mathRad,
		"log":        mathLog,
		"fmod":       mathFmod,
		"modf":       mathModf,
		"max":        mathMax,
		"min":        mathMin,
		"random":     mathRandom,
		"randomseed": mathRandomseed,
		"tointeger":  mathToInteger,
		"type":       mathType,
		"ult":        mathUlt,
	})
	m.rawset(MkString("pi"), Float(math.Pi))
	m.rawset(MkString("huge"), Float(math.Inf(1)))
	m.rawset(MkString("maxinteger"), Int(math.MaxInt64))
	m.rawset(MkString("mininteger"), Int(math.MinInt64))
	L.registerTable("math", m)
}

func math1(fn func(float64) float64) GoFunc {
	return func(L *LState) int {
		L.Push(Float(fn(L.checkNumber(1))))
		return 1
	}
}

func mathAbs(L *LState) int {
	v := L.Arg(1)
	if v.IsInt() {
		i := v.AsInt()
		if i < 0 {
			i = -i
		}
		L.Push(Int(i))
		return 1
	}
	L.Push(Float(math.Abs(L.checkNumber(1))))
	return 1
}

func mathCeil(L *LState) int {
	v := L.Arg(1)
	if v.IsInt() {
		L.Push(v)
		return 1
	}
	f := math.Ceil(L.checkNumber(1))
	// PUC pushnumint: integer result when it fits, otherwise the float itself.
	if i, ok := numberToInteger(f); ok {
		L.Push(Int(i))
	} else {
		L.Push(Float(f))
	}
	return 1
}

func mathFloor(L *LState) int {
	v := L.Arg(1)
	if v.IsInt() {
		L.Push(v)
		return 1
	}
	f := math.Floor(L.checkNumber(1))
	// PUC pushnumint: integer result when it fits, otherwise the float itself.
	if i, ok := numberToInteger(f); ok {
		L.Push(Int(i))
	} else {
		L.Push(Float(f))
	}
	return 1
}

func mathAtan(L *LState) int {
	y := L.checkNumber(1)
	x := 1.0
	if L.NArgs() >= 2 {
		x = L.checkNumber(2)
	}
	L.Push(Float(math.Atan2(y, x)))
	return 1
}

func mathLog(L *LState) int {
	x := L.checkNumber(1)
	if L.NArgs() >= 2 {
		base := L.checkNumber(2)
		switch base {
		case 2:
			L.Push(Float(math.Log2(x)))
		case 10:
			L.Push(Float(math.Log10(x)))
		default:
			L.Push(Float(math.Log(x) / math.Log(base)))
		}
		return 1
	}
	L.Push(Float(math.Log(x)))
	return 1
}

func mathDeg(L *LState) int {
	L.Push(Float(L.checkNumber(1) * (180.0 / math.Pi)))
	return 1
}

func mathRad(L *LState) int {
	L.Push(Float(L.checkNumber(1) * (math.Pi / 180.0)))
	return 1
}

func mathFmod(L *LState) int {
	a, b := L.Arg(1), L.Arg(2)
	// Both integers: integer modulo, with PUC's -1/0 special cases (math_fmod).
	if a.IsInt() && b.IsInt() {
		d := b.AsInt()
		if uint64(d)+1 <= 1 { // d == 0 or d == -1
			if d == 0 {
				L.argError(2, "zero")
			}
			L.Push(Int(0)) // avoid overflow with mininteger % -1
		} else {
			L.Push(Int(a.AsInt() % d))
		}
		return 1
	}
	L.Push(Float(math.Mod(L.checkNumber(1), L.checkNumber(2))))
	return 1
}

func mathModf(L *LState) int {
	if v := L.Arg(1); v.IsInt() {
		L.Push(v)        // an integer is its own integral part...
		L.Push(Float(0)) // ...with no fractional part
		return 2
	}
	arg := L.checkNumber(1)
	ip, fp := math.Modf(arg) // ip truncates toward zero, matching PUC
	if arg == ip {
		// integral value, including ±Inf (Go gives a NaN fractional part there)
		fp = 0.0
	}
	// PUC pushnumint: the integral part is an integer when it fits, else a
	// float (so ±Inf and magnitudes beyond int64 stay float).
	if i, ok := numberToInteger(ip); ok {
		L.Push(Int(i))
	} else {
		L.Push(Float(ip))
	}
	L.Push(Float(fp))
	return 2
}

// math_max / math_min use lua_compare(LUA_OPLT), so they preserve subtypes,
// honor __lt on non-numbers, and reject an empty arg list with "value expected".
func mathMax(L *LState) int {
	n := L.NArgs()
	if n < 1 {
		L.argError(1, "value expected")
	}
	imax := 1
	for i := 2; i <= n; i++ {
		if L.lessthan(L.Arg(imax), L.Arg(i)) {
			imax = i
		}
	}
	L.Push(L.Arg(imax))
	return 1
}

func mathMin(L *LState) int {
	n := L.NArgs()
	if n < 1 {
		L.argError(1, "value expected")
	}
	imin := 1
	for i := 2; i <= n; i++ {
		if L.lessthan(L.Arg(i), L.Arg(imin)) {
			imin = i
		}
	}
	L.Push(L.Arg(imin))
	return 1
}

// rngState holds the four 64-bit words of the xoshiro256** generator, ported
// bit-for-bit from PUC lmathlib.c so seeded sequences match reference Lua 5.4.
type rngState struct{ s [4]uint64 }

// rngSeedCounter perturbs the second auto-seed word so distinct states diverge
// (PUC mixes in the lua_State address; we lack a stable pointer value). It is
// bumped atomically because NewState — which calls newRNG — may run on several
// goroutines at once (e.g. a State pool), and a plain ++ would data-race.
var rngSeedCounter uint64

// newRNG builds an auto-seeded generator (PUC randseed: time + address).
func newRNG() *rngState {
	r := &rngState{}
	seq := atomic.AddUint64(&rngSeedCounter, 1)
	r.setseed(uint64(time.Now().UnixNano()), seq)
	return r
}

// rotl rotates x left by n bits (PUC rotl).
func rotl(x uint64, n int) uint64 { return (x << n) | (x >> (64 - n)) }

// nextrand advances the state and returns the next value (PUC nextrand,
// xoshiro256**).
func (r *rngState) nextrand() uint64 {
	s0, s1 := r.s[0], r.s[1]
	s2 := r.s[2] ^ s0
	s3 := r.s[3] ^ s1
	res := rotl(s1*5, 7) * 9
	r.s[0] = s0 ^ s3
	r.s[1] = s1 ^ s2
	r.s[2] = s2 ^ (s1 << 17)
	r.s[3] = rotl(s3, 45)
	return res
}

// setseed initialises the state from two seed words and discards 16 outputs to
// spread the seed (PUC setseed).
func (r *rngState) setseed(n1, n2 uint64) {
	r.s[0] = n1
	r.s[1] = 0xff // avoid a zero state
	r.s[2] = n2
	r.s[3] = 0
	for i := 0; i < 16; i++ {
		r.nextrand()
	}
}

// i2d converts a random word into a float in [0,1) (PUC I2d, FIGS=53 for
// double): take the top 53 bits and scale by 2^-53.
func i2d(x uint64) float64 {
	const shift = 64 - 53
	const scale = 0.5 / (1 << 52) // 2^-53
	return float64(x>>shift) * scale
}

// project maps a random word into [0, n] without modulo bias (PUC project):
// mask to the smallest 2^b-1 window covering n and re-draw on overshoot.
func (r *rngState) project(ran, n uint64) uint64 {
	if n&(n+1) == 0 { // n+1 is a power of 2 (also handles n == all-ones)
		return ran & n
	}
	lim := n
	lim |= lim >> 1
	lim |= lim >> 2
	lim |= lim >> 4
	lim |= lim >> 8
	lim |= lim >> 16
	lim |= lim >> 32
	for ran &= lim; ran > n; ran = r.nextrand() & lim {
	}
	return ran
}

// math_random: (), (m), (m,n), plus the 5.4 single-zero full-range form.
func mathRandom(L *LState) int {
	rv := L.rng.nextrand() // one draw per call, reused by the interval path
	var low, up int64
	switch L.NArgs() {
	case 0:
		L.Push(Float(i2d(rv))) // number in [0,1)
		return 1
	case 1:
		low = 1
		up = L.checkInt(1)
		if up == 0 { // math.random(0): a full-range random integer
			L.Push(Int(int64(rv)))
			return 1
		}
	case 2:
		low = L.checkInt(1)
		up = L.checkInt(2)
	default:
		L.errorf("wrong number of arguments")
	}
	if low > up {
		L.argError(1, "interval is empty")
	}
	// project the random value into [0, up-low] over the full unsigned range.
	p := L.rng.project(rv, uint64(up)-uint64(low))
	L.Push(Int(int64(p + uint64(low))))
	return 1
}

// math_randomseed: with args seed from (n1[,n2]); with none, auto-seed from the
// clock. Always returns the two seed components so a run can be reproduced.
func mathRandomseed(L *LState) int {
	if L.NArgs() >= 1 {
		n1 := L.checkInt(1)
		var n2 int64
		if L.NArgs() >= 2 {
			n2 = L.checkInt(2)
		}
		L.rng.setseed(uint64(n1), uint64(n2))
		L.Push(Int(n1))
		L.Push(Int(n2))
		return 2
	}
	rngSeedCounter++
	n1 := time.Now().UnixNano()
	n2 := int64(rngSeedCounter)
	L.rng.setseed(uint64(n1), uint64(n2))
	L.Push(Int(n1))
	L.Push(Int(n2))
	return 2
}

func mathToInteger(L *LState) int {
	v := L.Arg(1)
	// PUC lua_tointegerx routes through luaV_tointeger, which first coerces a
	// numerical string to its number (l_strton) before the integer check.
	if v.IsString() {
		if n, ok := str2num(v.Str()); ok {
			v = n
		}
	}
	if v.IsInt() {
		L.Push(Int(v.AsInt()))
		return 1
	}
	if v.IsFloat() {
		if i, ok := fltToIntEq(v.AsFloat()); ok {
			L.Push(Int(i))
			return 1
		}
	}
	L.Push(Nil)
	return 1
}

func mathType(L *LState) int {
	v := L.checkAny(1)
	switch {
	case v.IsInt():
		L.Push(MkString("integer"))
	case v.IsFloat():
		L.Push(MkString("float"))
	default:
		L.Push(Nil)
	}
	return 1
}

func mathUlt(L *LState) int {
	a := uint64(L.checkInt(1))
	b := uint64(L.checkInt(2))
	L.Push(Bool(a < b))
	return 1
}
