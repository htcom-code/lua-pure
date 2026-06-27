package luapure

// Weak collectable keys ('k' / 'kv' mode). A normal hash entry would retain its
// key through tkey.p (an unsafe.Pointer) and hentry.key, so a collectable key
// could never be reclaimed. Such keys are therefore kept out of the strong hash
// and stored here, holding the key object only through a weak.Pointer (weakCell)
// — when the key is collected the entry reads as absent and is skipped, matching
// PUC's weak-table clearing. Non-collectable keys (integers, strings) stay in
// the array/hash parts and are never weak. Lookups here are linear, which suits
// the small caches weak-key tables usually are.

// weakKeyEntry holds one collectable key (weakly) and its value. key == nil is a
// tombstone (a deleted slot); the slot is never physically removed so the index
// a paused next() captured stays valid.
type weakKeyEntry struct {
	key *weakCell // weak ref to the key object (origTag + alive); nil = tombstone
	val Value     // value, wrapVal-wrapped when the table is also weak-valued
}

// mkKeyCell wraps a collectable key as a weak cell (callers guarantee key is
// collectable, so mkWeak always wraps).
func mkKeyCell(key Value) *weakCell { return (*weakCell)(mkWeak(key).gc) }

// weakKeyIndex returns the slot holding live key `key`, or -1. Dead/tombstone
// slots are skipped.
func (t *Table) weakKeyIndex(key Value) int {
	for i := range t.weakKeys {
		c := t.weakKeys[i].key
		if c == nil {
			continue
		}
		if p, live := c.alive(); live && p == key.gc {
			return i
		}
	}
	return -1
}

// weakKeyGet returns t[key] for a collectable key in a weak-key table.
func (t *Table) weakKeyGet(key Value) Value {
	if i := t.weakKeyIndex(key); i >= 0 {
		return deref(t.weakKeys[i].val)
	}
	return Nil
}

// weakKeySet assigns t[key] = val for a collectable key in a weak-key table. A
// nil value tombstones the slot.
func (t *Table) weakKeySet(key, val Value) {
	val = t.wrapVal(val)
	i := t.weakKeyIndex(key)
	if val.IsNil() {
		if i >= 0 {
			t.weakKeys[i].key = nil // tombstone
			t.weakKeys[i].val = Nil
		}
		return
	}
	if i >= 0 {
		t.weakKeys[i].val = val
		return
	}
	t.weakKeys = append(t.weakKeys, weakKeyEntry{key: mkKeyCell(key), val: val})
}

// weakKeyAt returns the first live weak-key pair at or after slot idx, for
// next() iteration after the array and hash parts are exhausted.
func (t *Table) weakKeyAt(idx int) (nk, nv Value, ok bool) {
	for i := idx; i < len(t.weakKeys); i++ {
		c := t.weakKeys[i].key
		if c == nil {
			continue
		}
		p, live := c.alive()
		if !live {
			continue
		}
		if v := deref(t.weakKeys[i].val); !v.IsNil() {
			return Value{tag: c.origTag, gc: p}, v, true
		}
	}
	return Nil, Nil, false
}

// weakKeyNext continues next() from a collectable weak key.
func (t *Table) weakKeyNext(key Value) (nk, nv Value, ok, found bool) {
	i := t.weakKeyIndex(key)
	if i < 0 {
		return Nil, Nil, false, false
	}
	r1, r2, more := t.weakKeyAt(i + 1)
	return r1, r2, more, true
}

// migrateWeakKeys moves collectable keys between the strong hash and the weak
// store when __mode's 'k' bit flips (refreshWeak). PUC expects the mode to be
// set before insertion, but support a late change so a table keyed before
// setmetatable still behaves.
func (t *Table) migrateWeakKeys(toWeak bool) {
	if toWeak {
		if t.hash == nil {
			return
		}
		for _, k := range t.keys {
			if !collectableTag(k.tag) {
				continue
			}
			e, live := t.hash[k]
			if !live || e.val.IsNil() {
				continue
			}
			t.weakKeys = append(t.weakKeys, weakKeyEntry{key: mkKeyCell(e.key), val: e.val})
			t.hashdel(k)
		}
		return
	}
	// weak -> strong: materialize surviving keys back into the hash.
	for i := range t.weakKeys {
		c := t.weakKeys[i].key
		if c == nil {
			continue
		}
		if p, live := c.alive(); live {
			key := Value{tag: c.origTag, gc: p}
			nk, _ := normKey(key)
			t.hashset(nk, key, deref(t.weakKeys[i].val))
		}
	}
	t.weakKeys = nil
}
