package luapure

// Public full-userdata API for embedders: wrap an arbitrary Go value as a Lua
// object, give it a metatable of methods, and unwrap it again — type-checked —
// inside the GoFuncs that implement those methods. This is the luapure analogue
// of the C embedding idiom built from lua_newuserdatauv + luaL_newmetatable +
// luaL_checkudata, the standard way a host binds one of its own types into Lua.
//
// A typical binding registers a named metatable once, fills in its method
// table, then mints userdata against it:
//
//	mt, created := L.NewMetatable("File")
//	if created {
//	    methods := luapure.NewTable()
//	    methods.SetStr("read", luapure.NewGoFunc("read", fileRead))
//	    mt.SetStr("__index", methods.Value())
//	}
//	h := L.NewUserData(&File{...}, mt)   // hand h to a script
//
// and each method recovers the Go value with the matching type check:
//
//	func fileRead(L *luapure.LState) int {
//	    f := L.CheckUserData(1, "File").(*File)
//	    ...
//	}

// NewUserData wraps an arbitrary Go value as full userdata carrying meta as its
// metatable (nil for none). The payload is opaque to Lua scripts, which reach
// it only through the metatable (e.g. __index methods, __gc). If meta carries a
// __gc field a finalizer is registered the same way lua_setmetatable does, so
// the embedder's __gc runs when the object becomes unreachable.
func (L *LState) NewUserData(data any, meta *Table) Value {
	return L.NewUserDataUV(data, 0, meta)
}

// NewUserDataUV is NewUserData with nuv associated Lua-value slots (uservalues),
// the lua_newuserdatauv form. The slots start nil and are read and written with
// UserValue / SetUserValue (and the script-facing debug.getuservalue/
// setuservalue). Unlike the Go payload, a uservalue is a Lua value the GC tracks
// — use it to keep a Lua object (a callback, a config table) alive alongside the
// userdatum and reachable from it without a separate registry entry.
func (L *LState) NewUserDataUV(data any, nuv int, meta *Table) Value {
	u := &userData{data: data, meta: meta}
	if nuv > 0 {
		u.uv = make([]Value, nuv)
	}
	v := mkUserData(u)
	if meta != nil {
		L.checkFinalizer(v)
	}
	return v
}

// AsUserData returns the Go payload of a full userdata Value, or nil when v is
// not full userdata (lua_touserdata). Light userdata yields nil — it carries no
// Go payload. Pair it with IsUserData to tell a nil payload from a non-userdata.
func (v Value) AsUserData() any {
	if v.tag != tagUserData {
		return nil
	}
	return v.userData().data
}

// UserMetatable returns the metatable attached to a full userdata Value, or nil
// (when v is not userdata or has none).
func (v Value) UserMetatable() *Table {
	if v.tag != tagUserData {
		return nil
	}
	return v.userData().meta
}

// NumUserValues reports how many uservalue slots v was created with, or 0 when
// v is not full userdata.
func (v Value) NumUserValues() int {
	if v.tag != tagUserData {
		return 0
	}
	return len(v.userData().uv)
}

// UserValue returns the n-th uservalue slot (1-based) of a full userdata, the
// lua_getiuservalue form. ok is false when v is not userdata or n is out of
// range, in which case the value is Nil.
func (v Value) UserValue(n int) (val Value, ok bool) {
	if v.tag != tagUserData {
		return Nil, false
	}
	uv := v.userData().uv
	if n < 1 || n > len(uv) {
		return Nil, false
	}
	return uv[n-1], true
}

// SetUserValue assigns the n-th uservalue slot (1-based) of a full userdata,
// the lua_setiuservalue form. It reports false (assigning nothing) when v is not
// userdata or n is out of range.
func (v Value) SetUserValue(n int, val Value) bool {
	if v.tag != tagUserData {
		return false
	}
	uv := v.userData().uv
	if n < 1 || n > len(uv) {
		return false
	}
	uv[n-1] = val
	return true
}

// SetUserMetatable replaces a full userdata's metatable (nil to clear it),
// registering a __gc finalizer if the new metatable carries one — the
// lua_setmetatable path. It is a no-op for non-userdata values.
func (L *LState) SetUserMetatable(v Value, meta *Table) {
	if v.tag != tagUserData {
		return
	}
	v.userData().meta = meta
	if meta != nil {
		L.checkFinalizer(v)
	}
}

// --- named metatables in the registry (luaL_newmetatable family) ---

// NewMetatable returns the metatable registered under name in the registry,
// creating an empty one — with its __name set to name — on the first call.
// created reports whether this call made it, so an embedder installs the
// methods exactly once (luaL_newmetatable). The name doubles as the type label
// in "bad argument" errors raised by CheckUserData.
func (L *LState) NewMetatable(name string) (mt *Table, created bool) {
	if v := L.registry.rawgetStr(name); v.IsTable() {
		return v.tablev(), false
	}
	mt = newTable()
	mt.rawset(MkString("__name"), MkString(name))
	L.registry.rawset(MkString(name), mkTable(mt))
	return mt, true
}

// GetMetatable returns the registry metatable registered under name, or nil
// when NewMetatable has not created it yet (luaL_getmetatable).
func (L *LState) GetMetatable(name string) *Table {
	if v := L.registry.rawgetStr(name); v.IsTable() {
		return v.tablev()
	}
	return nil
}

// CheckUserData returns argument n's Go payload after verifying its metatable
// is the registry metatable named name; otherwise it raises a "bad argument"
// type error reporting name as the expected type (luaL_checkudata). Use it to
// safely unwrap the Go value a method received as self.
func (L *LState) CheckUserData(n int, name string) any {
	d, ok := L.testUserData(n, name)
	if !ok {
		L.typeArgError(n, name)
	}
	return d
}

// TestUserData is the non-raising CheckUserData (luaL_testudata): it returns the
// payload when argument n is full userdata whose metatable is the registry
// metatable named name, else nil. A nil result is also returned for a userdata
// whose payload is genuinely nil; when that distinction matters, compare the
// argument's UserMetatable against GetMetatable directly.
func (L *LState) TestUserData(n int, name string) any {
	d, _ := L.testUserData(n, name)
	return d
}

// testUserData reports whether argument n is full userdata tagged with the
// registry metatable named name, returning its payload when so.
func (L *LState) testUserData(n int, name string) (any, bool) {
	v := L.Arg(n)
	if v.tag != tagUserData {
		return nil, false
	}
	u := v.userData()
	if u.meta == nil {
		return nil, false
	}
	reg := L.registry.rawgetStr(name)
	if !reg.IsTable() || reg.tablev() != u.meta {
		return nil, false
	}
	return u.data, true
}
