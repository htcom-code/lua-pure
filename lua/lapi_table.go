package luapure

// Public table API for embedders: create tables, and read/write fields from Go.
// The Table methods are raw (they ignore metamethods, like lua_rawget/rawset);
// for metamethod-aware access use (*LState).Index / SetIndex. This wraps the
// internal raw* helpers, adding no new semantics.

// NewTable returns a fresh empty table.
func NewTable() *Table { return newTable() }

// AsTable returns the underlying *Table when v is a table, else nil. Pair it
// with IsTable to distinguish a non-table from an empty table.
func (v Value) AsTable() *Table {
	if v.tag != tagTable {
		return nil
	}
	return v.tablev()
}

// Value wraps the table as a Lua Value (for SetGlobal, Set, call arguments…).
func (t *Table) Value() Value { return mkTable(t) }

// Get returns t[key] without invoking metamethods (raw access).
func (t *Table) Get(key Value) Value { return t.rawget(key) }

// Set assigns t[key] = val without invoking metamethods (raw access). Setting a
// nil value removes the key.
func (t *Table) Set(key, val Value) { t.rawset(key, val) }

// GetStr returns t[name] for a string key.
func (t *Table) GetStr(name string) Value { return t.rawgetStr(name) }

// SetStr assigns t[name] = val for a string key.
func (t *Table) SetStr(name string, val Value) { t.rawset(MkString(name), val) }

// GetInt returns t[i] for an integer key.
func (t *Table) GetInt(i int64) Value { return t.rawgetInt(i) }

// SetInt assigns t[i] = val for an integer key.
func (t *Table) SetInt(i int64, val Value) { t.rawsetInt(i, val) }

// Len returns the table's border length (the # operator's raw result).
func (t *Table) Len() int64 { return t.length() }

// Next iterates raw key/value pairs the way Lua's next does: pass Nil to start,
// then pass back the previous key. ok is false once iteration is exhausted.
// Like next, the traversal order is unspecified and the table must not have
// keys inserted during iteration (assigning nil to the current key is allowed).
func (t *Table) Next(key Value) (k, v Value, ok bool) {
	nk, nv, more, _ := t.next(key)
	return nk, nv, more
}

// Index returns t[key] honouring the __index metamethod chain (the indexing a
// Lua program sees). t may be any value; a non-indexable value raises an error.
func (L *LState) Index(t, key Value) Value { return L.indexGet(t, key) }

// SetIndex assigns t[key] = val honouring the __newindex metamethod chain.
func (L *LState) SetIndex(t, key, val Value) { L.settable(t, key, val) }
