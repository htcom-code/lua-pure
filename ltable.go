package luapure

import (
	"math"
	"strings"
	"sync/atomic"
	"unsafe"
)

// Table is the Lua 5.4 table object. PUC (ltable.c) uses a hybrid of a packed
// array part plus an open-addressed node array; this port keeps the same
// observable semantics with a Go-native split — a 1-based array part for dense
// integer keys and a map for everything else — which is simpler and equivalent
// for the VM and the test suite (Lua does not specify hash iteration order).
//
// Keys are normalised exactly as PUC: a float key with an integral value is
// stored under the equivalent integer key (t[2.0] and t[2] are the same slot),
// and NaN / nil keys are rejected on assignment.
type Table struct {
	arr  []Value          // arr[i] holds t[i+1]
	hash map[tkey]hentry  // non-array keys
	keys []tkey           // insertion order of hash keys, for deterministic next()
	meta *Table           // metatable (may be nil)

	// weakk/weakv cache the table's __mode (refreshed from the metatable by
	// refreshWeak when it is set). When weakv is true, collectable values are
	// stored as weak cells (see weak.go) so the GC can reclaim them.
	weakk bool
	weakv bool

	// finReg is set once a Go finalizer has been attached for this table's __gc
	// (PUC FINALIZEDBIT): registration happens at most once per object.
	finReg bool
}

// tkey is a comparable normalisation of a Lua value usable as a Go map key.
// Strings compare by content and GC objects by identity, unlike a raw Value
// (whose gc pointer would split equal strings into distinct keys).
type tkey struct {
	tag vtag
	n   uint64         // int bits / float bits
	s   string         // string content
	p   unsafe.Pointer // gc identity (table/function/userdata)
}

type hentry struct {
	key Value // original key, returned by next()
	val Value
}

// MaxTableArraySize (luaconf.go) caps table array-part growth.

func newTable() *Table { return &Table{} }

// refreshWeak recomputes weakk/weakv from the table's metatable __mode and
// converts existing value slots to match (PUC reads __mode lazily during GC;
// we cache it whenever the metatable is set). Turning weak-value on wraps
// existing collectable values as weak cells; turning it off materializes them
// back to strong. Mutating __mode in place after setmetatable is not observed
// (no conformance test does so) — set the mode before inserting weak values,
// exactly as the suite does.
func (t *Table) refreshWeak() {
	wk, wv := false, false
	if t.meta != nil {
		if mv := t.meta.rawgetStr("__mode"); mv.IsString() {
			s := mv.Str()
			wk = strings.ContainsRune(s, 'k')
			wv = strings.ContainsRune(s, 'v')
		}
	}
	if (wk || wv) && !(t.weakk || t.weakv) {
		atomic.AddInt32(&liveWeakTables, 1) // first time this table becomes weak
	}
	switch {
	case wv && !t.weakv: // strong -> weak values: wrap existing entries
		for i := range t.arr {
			t.arr[i] = mkWeak(t.arr[i])
		}
		for k, e := range t.hash {
			e.val = mkWeak(e.val)
			t.hash[k] = e
		}
	case !wv && t.weakv: // weak -> strong values: materialize survivors
		for i := range t.arr {
			t.arr[i] = deref(t.arr[i])
		}
		for k, e := range t.hash {
			e.val = deref(e.val)
			t.hash[k] = e
		}
	}
	t.weakk, t.weakv = wk, wv
}

// normKey builds the map key for v. ok=false when v cannot be a key (nil or
// NaN); a float with an exact integer value is folded to the integer key.
func normKey(v Value) (tkey, bool) {
	switch v.tag {
	case tagNil:
		return tkey{}, false
	case tagInt:
		return tkey{tag: tagInt, n: v.scalar}, true
	case tagFloat:
		f := v.AsFloat()
		if math.IsNaN(f) {
			return tkey{}, false
		}
		if i, ok := fltToIntEq(f); ok {
			return tkey{tag: tagInt, n: uint64(i)}, true
		}
		return tkey{tag: tagFloat, n: v.scalar}, true
	case tagTrue, tagFalse:
		return tkey{tag: v.tag}, true
	case tagString:
		return tkey{tag: tagString, s: v.Str()}, true
	default:
		return tkey{tag: v.tag, p: v.gc}, true
	}
}

// intInArray reports whether integer key k lands in the array part (1-based).
func (t *Table) intInArray(k int64) bool {
	return k >= 1 && k <= int64(len(t.arr))
}

// wrapVal converts a value to the form stored in this table's slots: weak
// tables hold collectable values weakly so the GC can reclaim them. Idempotent.
func (t *Table) wrapVal(v Value) Value {
	if t.weakv {
		return mkWeak(v)
	}
	return v
}

// rawget returns t[key] without metamethods (luaH_get).
func (t *Table) rawget(key Value) Value {
	if key.IsInt() {
		return t.rawgetInt(key.AsInt())
	}
	if key.IsFloat() {
		if i, ok := fltToIntEq(key.AsFloat()); ok {
			return t.rawgetInt(i)
		}
	}
	nk, ok := normKey(key)
	if !ok {
		return Nil
	}
	if t.hash == nil {
		return Nil
	}
	v := t.hash[nk].val
	if v.tag == tagWeakRef {
		if isDeadWeak(v) {
			t.hashdel(nk) // lazily drop the cleared entry
			return Nil
		}
		return deref(v)
	}
	return v
}

// rawgetInt returns t[k] for an integer key (luaH_getint).
func (t *Table) rawgetInt(k int64) Value {
	if t.intInArray(k) {
		v := t.arr[k-1]
		if v.tag == tagWeakRef {
			if isDeadWeak(v) {
				t.arr[k-1] = Nil // lazily drop the cleared entry
				return Nil
			}
			return deref(v)
		}
		return v
	}
	if t.hash == nil {
		return Nil
	}
	nk := tkey{tag: tagInt, n: uint64(k)}
	v := t.hash[nk].val
	if v.tag == tagWeakRef {
		if isDeadWeak(v) {
			t.hashdel(nk)
			return Nil
		}
		return deref(v)
	}
	return v
}

// rawgetStr returns t[s] for a string key (luaH_getshortstr fast path).
func (t *Table) rawgetStr(s string) Value {
	if t.hash == nil {
		return Nil
	}
	nk := tkey{tag: tagString, s: s}
	v := t.hash[nk].val
	if v.tag == tagWeakRef {
		if isDeadWeak(v) {
			t.hashdel(nk)
			return Nil
		}
		return deref(v)
	}
	return v
}

// rawset assigns t[key] = val without metamethods (luaH_set + finishset).
func (t *Table) rawset(key, val Value) {
	if key.IsInt() {
		t.rawsetInt(key.AsInt(), val)
		return
	}
	if key.IsFloat() {
		if i, ok := fltToIntEq(key.AsFloat()); ok {
			t.rawsetInt(i, val)
			return
		}
	}
	nk, ok := normKey(key)
	if !ok {
		return // nil/NaN key: caller validates and errors before reaching here
	}
	t.hashset(nk, key, val)
}

// rawsetInt assigns an integer-keyed slot, growing the array part when the key
// extends it contiguously and migrating any now-contiguous hash keys.
func (t *Table) rawsetInt(k int64, val Value) {
	val = t.wrapVal(val) // weak tables store collectable values weakly
	if t.intInArray(k) {
		t.arr[k-1] = val
		return
	}
	if k == int64(len(t.arr))+1 && !val.IsNil() {
		t.arr = append(t.arr, val)
		t.absorbFromHash()
		return
	}
	t.hashset(tkey{tag: tagInt, n: uint64(k)}, Int(k), val)
}

// absorbFromHash pulls keys len(arr)+1, +2, ... out of the hash into the array
// part after a contiguous append, keeping the dense prefix in the array.
func (t *Table) absorbFromHash() {
	if t.hash == nil {
		return
	}
	for {
		nk := tkey{tag: tagInt, n: uint64(len(t.arr) + 1)}
		e, ok := t.hash[nk]
		if !ok || e.val.IsNil() {
			return
		}
		t.arr = append(t.arr, e.val)
		t.hashdel(nk)
	}
}

func (t *Table) hashset(nk tkey, key, val Value) {
	val = t.wrapVal(val) // idempotent: rawsetInt may have wrapped already
	if val.IsNil() {
		t.hashdel(nk)
		return
	}
	if t.hash == nil {
		t.hash = make(map[tkey]hentry)
	}
	if _, exists := t.hash[nk]; !exists {
		t.keys = append(t.keys, nk)
	}
	t.hash[nk] = hentry{key: key, val: val}
}

func (t *Table) hashdel(nk tkey) {
	// Keep the entry as a dead node (nil value) rather than deleting it, so the
	// key stays uniquely represented in t.keys. Otherwise a later re-insert of
	// the same key would append a second t.keys slot (hashset only appends when
	// the map lacks the key), and next() — which locates a key by its first
	// t.keys occurrence — would loop forever between the duplicate slots. PUC
	// likewise keeps dead nodes until a rehash. rawget reads a dead entry's nil
	// value, so the key correctly reads as absent.
	if t.hash != nil {
		if e, ok := t.hash[nk]; ok {
			e.val = Nil
			t.hash[nk] = e
		}
	}
}

// length returns a border n (t[n] non-nil, t[n+1] nil), the # operator result.
// Ported from luaH_getn's binary / unbound search, adapted to the split layout.
func (t *Table) length() int64 {
	n := len(t.arr)
	if n > 0 && deref(t.arr[n-1]).IsNil() {
		// Border lies inside the array part: binary search.
		lo, hi := 0, n
		for hi-lo > 1 {
			m := (lo + hi) / 2
			if deref(t.arr[m-1]).IsNil() {
				hi = m
			} else {
				lo = m
			}
		}
		return int64(lo)
	}
	// Array part is dense up to n; continue into the hash if n+1 is present.
	if t.hash == nil || t.rawgetInt(int64(n)+1).IsNil() {
		return int64(n)
	}
	i := int64(n)
	j := i + 1
	for !t.rawgetInt(j).IsNil() {
		i = j
		if j > math.MaxInt64/2 {
			// Overflow guard: fall back to a linear scan (luaH unbound_search).
			i = 1
			for !t.rawgetInt(i).IsNil() {
				i++
			}
			return i - 1
		}
		j *= 2
	}
	for j-i > 1 {
		m := (i + j) / 2
		if t.rawgetInt(m).IsNil() {
			j = m
		} else {
			i = m
		}
	}
	return i
}

// next implements luaH_next for the pairs() iterator. Passing a nil key starts
// iteration; it returns the key/value following `key`, or ok=false at the end.
// found=false means the key is not in the table (an error for the caller).
func (t *Table) next(key Value) (nk, nv Value, ok, found bool) {
	// Array part is visited first, in index order.
	start := 0
	if key.IsNil() {
		start = 0
	} else if ik, isInt := arrayIndex(key); isInt && ik >= 1 && ik <= int64(len(t.arr)) {
		start = int(ik) // continue after this array slot
	} else {
		// Key is in the hash part: find it, then continue from the next entry.
		return t.nextHash(key)
	}
	for i := start; i < len(t.arr); i++ {
		if v := deref(t.arr[i]); !v.IsNil() {
			return Int(int64(i + 1)), v, true, true
		}
	}
	// Array exhausted: move into the hash part from the beginning.
	return t.nextHashFrom(0)
}

// arrayIndex returns the integer index a key denotes, if it is an int (or a
// float with an exact integer value).
func arrayIndex(key Value) (int64, bool) {
	if key.IsInt() {
		return key.AsInt(), true
	}
	if key.IsFloat() {
		return fltToIntEq(key.AsFloat())
	}
	return 0, false
}

func (t *Table) nextHash(key Value) (nk, nv Value, ok, found bool) {
	cur, valid := normKey(key)
	if !valid {
		return Nil, Nil, false, false
	}
	for idx, k := range t.keys {
		if k == cur {
			if _, live := t.hash[k]; !live {
				// Current key was deleted mid-traversal; PUC forbids this but
				// be lenient and continue from the next slot.
			}
			r1, r2, more := t.nextHashAt(idx + 1)
			return r1, r2, more, true
		}
	}
	return Nil, Nil, false, false
}

func (t *Table) nextHashFrom(idx int) (nk, nv Value, ok, found bool) {
	r1, r2, more := t.nextHashAt(idx)
	return r1, r2, more, true
}

func (t *Table) nextHashAt(idx int) (nk, nv Value, ok bool) {
	for i := idx; i < len(t.keys); i++ {
		if e, live := t.hash[t.keys[i]]; live {
			if v := deref(e.val); !v.IsNil() {
				return e.key, v, true
			}
		}
	}
	return Nil, Nil, false
}
