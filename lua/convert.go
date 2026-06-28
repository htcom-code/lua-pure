package luapure

// Go <-> Lua value conversion convenience for embedders. The supported set is
// deliberately small (the primitives plus slices and maps); functions, threads,
// and userdata pass through untouched. Anything outside the set converts to nil
// rather than panicking.

// ToValue converts a Go value into a Lua Value. Supported: nil, bool, the
// integer and float kinds, string, []any (a 1-based array table), and
// map[string]any / map[any]any (a keyed table); nested slices/maps recurse. A
// Value passes through unchanged. Cyclic Go structures are not supported.
func (L *LState) ToValue(x any) Value {
	switch v := x.(type) {
	case nil:
		return Nil
	case Value:
		return v
	case bool:
		return Bool(v)
	case int:
		return Int(int64(v))
	case int8:
		return Int(int64(v))
	case int16:
		return Int(int64(v))
	case int32:
		return Int(int64(v))
	case int64:
		return Int(v)
	case uint:
		return Int(int64(v))
	case uint8:
		return Int(int64(v))
	case uint16:
		return Int(int64(v))
	case uint32:
		return Int(int64(v))
	case uint64:
		return Int(int64(v))
	case float32:
		return Float(float64(v))
	case float64:
		return Float(v)
	case string:
		return MkString(v)
	case []any:
		t := newTable()
		for i, e := range v {
			t.rawsetInt(int64(i+1), L.ToValue(e))
		}
		return mkTable(t)
	case map[string]any:
		t := newTable()
		for k, e := range v {
			t.rawset(MkString(k), L.ToValue(e))
		}
		return mkTable(t)
	case map[any]any:
		t := newTable()
		for k, e := range v {
			t.rawset(L.ToValue(k), L.ToValue(e))
		}
		return mkTable(t)
	default:
		return Nil
	}
}

// FromValue converts a Lua Value into a Go value: nil, bool, int64, float64,
// string, or — for a table — a map[any]any whose keys and values are themselves
// converted (shared/cyclic tables map to the same Go map). Functions, threads,
// and userdata are returned as their Value unchanged.
func FromValue(v Value) any {
	return fromValue(v, nil)
}

func fromValue(v Value, seen map[*Table]map[any]any) any {
	switch v.tag {
	case tagNil:
		return nil
	case tagTrue:
		return true
	case tagFalse:
		return false
	case tagInt:
		return v.AsInt()
	case tagFloat:
		return v.AsFloat()
	case tagString:
		return v.Str()
	case tagTable:
		return tableToMap(v.tablev(), seen)
	default:
		return v
	}
}

func tableToMap(t *Table, seen map[*Table]map[any]any) map[any]any {
	if m, ok := seen[t]; ok {
		return m // already converting/converted: break the cycle
	}
	if seen == nil {
		seen = make(map[*Table]map[any]any)
	}
	m := make(map[any]any)
	seen[t] = m
	k := Nil
	for {
		nk, nv, ok, _ := t.next(k)
		if !ok {
			break
		}
		k = nk
		m[mapKey(nk, seen)] = fromValue(nv, seen)
	}
	return m
}

// mapKey converts a Lua table key to a comparable Go key. Primitive keys use
// their Go scalar; table/function/other keys keep the raw Value (which is
// comparable) so a non-comparable map/slice never reaches a Go map key.
func mapKey(k Value, seen map[*Table]map[any]any) any {
	switch k.tag {
	case tagNil, tagTrue, tagFalse, tagInt, tagFloat, tagString:
		return fromValue(k, seen)
	default:
		return k
	}
}
