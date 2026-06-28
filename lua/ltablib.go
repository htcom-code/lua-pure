package luapure

import (
	"math"
	"strings"
)

// The table library (ltablib.c).

func (L *LState) OpenTable() {
	t := newTable()
	setFuncs(t, map[string]GoFunc{
		"insert": tblInsert,
		"remove": tblRemove,
		"concat": tblConcat,
		"unpack": tblUnpack,
		"pack":   tblPack,
		"sort":   tblSort,
		"move":   tblMove,
	})
	L.registerTable("table", t)
}

func tblInsert(L *LState) int {
	tv := L.checkTableVal(1)
	e := L.lenOf(tv) + 1 // first empty slot (wraps like PUC for a huge __len)
	switch L.NArgs() {
	case 2:
		L.settable(tv, Int(e), L.Arg(2))
	case 3:
		pos := L.checkInt(2)
		// position must be in [1, e]; the unsigned wrap catches pos <= 0.
		if uint64(pos)-1 >= uint64(e) {
			L.argError(2, "position out of bounds")
		}
		for i := e; i > pos; i-- {
			L.settable(tv, Int(i), L.indexGet(tv, Int(i-1)))
		}
		L.settable(tv, Int(pos), L.Arg(3))
	default:
		L.errorf("wrong number of arguments to 'insert'")
	}
	return 0
}

func tblRemove(L *LState) int {
	tv := L.checkTableVal(1)
	size := L.lenOf(tv)
	pos := L.optInt(2, size)
	if pos != size { // validate an explicit position (PUC tremove)
		// 1 <= pos <= size+1; the unsigned wrap rejects pos <= 0.
		if uint64(pos)-1 > uint64(size) {
			L.argError(2, "position out of bounds")
		}
	}
	v := L.indexGet(tv, Int(pos))
	for ; pos < size; pos++ {
		L.settable(tv, Int(pos), L.indexGet(tv, Int(pos+1)))
	}
	L.settable(tv, Int(pos), Nil)
	L.Push(v)
	return 1
}

func tblConcat(L *LState) int {
	tv := L.checkTableVal(1)
	sep := ""
	if L.NArgs() >= 2 && !L.Arg(2).IsNil() {
		sep = L.checkString(2)
	}
	i := L.optInt(3, 1)
	j := L.optInt(4, L.lenOf(tv))
	var sb strings.Builder
	addField := func(idx int64) {
		v := L.indexGet(tv, Int(idx))
		if !v.IsString() && !v.IsNumber() {
			L.errorf("invalid value (%s) at index %d in table for 'concat'", typeName(v), idx)
		}
		sb.WriteString(tostr(v))
	}
	// Loop i < j then add the last field (PUC tconcat), so j == math.maxinteger
	// doesn't overflow the loop variable.
	for ; i < j; i++ {
		addField(i)
		sb.WriteString(sep)
	}
	if i == j { // non-empty interval: add the final element
		addField(i)
	}
	L.Push(MkString(sb.String()))
	return 1
}

func tblUnpack(L *LState) int {
	tv := L.checkTableVal(1)
	i := L.optInt(2, 1)
	j := L.optInt(3, L.lenOf(tv))
	if i > j {
		return 0 // empty range
	}
	// Reject ranges that won't fit on the stack (ltablib.c: lua_checkstack for
	// the result count fails). n is count-1, computed unsigned to avoid overflow.
	n := uint64(j) - uint64(i)
	if n >= uint64(L.cfg.maxStack) || L.top+int(n)+1 > L.cfg.maxStack {
		L.errorf("too many results to unpack") // lua_checkstack(n+1) would fail
	}
	// Pre-grow once so the per-push checkstack never raises "stack overflow"
	// instead of the message above.
	L.checkstack(int(n) + 1)
	// Push i..j-1 then j separately (PUC), so j == math.maxinteger doesn't make
	// i++ overflow past j into an endless loop.
	for ; i < j; i++ {
		L.Push(L.indexGet(tv, Int(i)))
	}
	L.Push(L.indexGet(tv, Int(j)))
	return int(n) + 1
}

func tblPack(L *LState) int {
	n := L.NArgs()
	t := newTable()
	for i := 1; i <= n; i++ {
		t.rawsetInt(int64(i), L.Arg(i))
	}
	t.rawset(MkString("n"), Int(int64(n)))
	L.Push(mkTable(t))
	return 1
}

func tblSort(L *LState) int {
	tv := L.checkTableVal(1)
	n := int(L.lenOf(tv))
	if n <= 1 {
		return 0
	}
	if int64(n) >= 0x7FFFFFFF {
		L.argError(1, "array too big")
	}
	hasComp := L.NArgs() >= 2 && !L.Arg(2).IsNil()
	comp := L.Arg(2)
	if hasComp && !comp.IsFunction() { // must be a function (luaL_checktype)
		L.typeArgError(2, "function")
	}
	a := make([]Value, n+1) // 1-indexed: a[1..n], matching PUC
	for i := 1; i <= n; i++ {
		a[i] = L.indexGet(tv, Int(int64(i)))
	}
	lt := func(x, y Value) bool {
		if hasComp {
			res := L.callNoYield(comp, []Value{x, y}, 1)
			return len(res) > 0 && !res[0].IsFalsy()
		}
		return L.lessthan(x, y)
	}
	L.auxsort(a, 1, n, lt)
	for i := 1; i <= n; i++ {
		L.settable(tv, Int(int64(i)), a[i])
	}
	return 0
}

func swapVals(a []Value, i, j int) { a[i], a[j] = a[j], a[i] }

// auxsort is ltablib.c's auxsort: median-of-three quicksort with tail recursion
// on the larger partition. Pivot is always the middle element (PUC's rnd==0
// path); randomization only affects worst-case performance, not correctness.
func (L *LState) auxsort(a []Value, lo, up int, lt func(x, y Value) bool) {
	for lo < up {
		if lt(a[up], a[lo]) { // a[up] < a[lo]?
			swapVals(a, lo, up)
		}
		if up-lo == 1 {
			return
		}
		p := (lo + up) / 2
		if lt(a[p], a[lo]) { // a[p] < a[lo]?
			swapVals(a, p, lo)
		} else if lt(a[up], a[p]) { // a[up] < a[p]?
			swapVals(a, p, up)
		}
		if up-lo == 2 {
			return
		}
		swapVals(a, p, up-1) // move pivot to a[up-1]
		pp := L.partition(a, lo, up, lt)
		if pp-lo < up-pp { // recurse into the smaller side, iterate the larger
			L.auxsort(a, lo, pp-1, lt)
			lo = pp + 1
		} else {
			L.auxsort(a, pp+1, up, lt)
			up = pp - 1
		}
	}
}

// partition is ltablib.c's partition; it raises "invalid order function for
// sorting" when the comparator is inconsistent (Hoare-style with sentinels).
func (L *LState) partition(a []Value, lo, up int, lt func(x, y Value) bool) int {
	P := a[up-1] // pivot
	i := lo
	j := up - 1
	for {
		i++
		for lt(a[i], P) { // repeat ++i while a[i] < P
			if i == up-1 {
				L.errorf("invalid order function for sorting")
			}
			i++
		}
		j--
		for lt(P, a[j]) { // repeat --j while P < a[j]
			if j < i {
				L.errorf("invalid order function for sorting")
			}
			j--
		}
		if j < i {
			swapVals(a, up-1, i) // place pivot
			return i
		}
		swapVals(a, i, j)
	}
}

func tblMove(L *LState) int {
	a1 := L.checkTableVal(1)
	f := L.checkInt(2)
	e := L.checkInt(3)
	tpos := L.checkInt(4)
	a2 := a1
	if L.NArgs() >= 5 && !L.Arg(5).IsNil() {
		a2 = L.checkTableVal(5)
	}
	if e >= f { // otherwise nothing to move (PUC tmove)
		// guard the element count and destination against integer overflow
		if !(f > 0 || e < math.MaxInt64+f) {
			L.argError(3, "too many elements to move")
		}
		n := e - f + 1 // number of elements
		if tpos > math.MaxInt64-n+1 {
			L.argError(4, "destination wrap around")
		}
		// Iterate a 0..n-1 count using f+i / tpos+i so e == math.maxinteger does
		// not overflow the loop variable. Move backward only when ranges overlap
		// within the same table.
		if tpos > e || tpos <= f || a1.tablev() != a2.tablev() {
			for i := int64(0); i < n; i++ {
				L.settable(a2, Int(tpos+i), L.indexGet(a1, Int(f+i)))
			}
		} else {
			for i := n - 1; i >= 0; i-- {
				L.settable(a2, Int(tpos+i), L.indexGet(a1, Int(f+i)))
			}
		}
	}
	L.Push(a2)
	return 1
}
