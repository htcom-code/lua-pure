package luapure

// Public argument-checking helpers for Go callbacks (the luaL_check*/luaL_opt*
// family). They report a "bad argument" error with the caller's position the
// same way the standard libraries do, so embedder GoFuncs validate arguments
// exactly like the built-ins. Argument indices are 1-based (Arg's convention).

// CheckString returns argument n as a string, coercing a number, else raises a
// "bad argument" error (luaL_checkstring).
func (L *LState) CheckString(n int) string { return L.checkString(n) }

// CheckInt returns argument n as an integer, else raises an error. A number
// with no integer representation is rejected (luaL_checkinteger).
func (L *LState) CheckInt(n int) int64 { return L.checkInt(n) }

// CheckNumber returns argument n as a float, else raises an error
// (luaL_checknumber).
func (L *LState) CheckNumber(n int) float64 { return L.checkNumber(n) }

// CheckTable returns argument n as a *Table, else raises an error
// (luaL_checktype with LUA_TTABLE).
func (L *LState) CheckTable(n int) *Table { return L.checkTable(n) }

// CheckBool returns argument n when it is a boolean, else raises an error.
func (L *LState) CheckBool(n int) bool {
	v := L.Arg(n)
	if v.IsBool() {
		return v.AsBool()
	}
	L.typeArgError(n, "boolean")
	return false
}

// OptInt returns argument n as an integer, or def when it is absent or nil
// (luaL_optinteger).
func (L *LState) OptInt(n int, def int64) int64 { return L.optInt(n, def) }

// OptString returns argument n as a string, or def when it is absent or nil
// (luaL_optstring).
func (L *LState) OptString(n int, def string) string {
	if n > L.NArgs() || L.Arg(n).IsNil() {
		return def
	}
	return L.checkString(n)
}

// ArgError raises a "bad argument #n to '<func>' (<msg>)" error for the current
// native call (luaL_argerror).
func (L *LState) ArgError(n int, msg string) { L.argError(n, msg) }

// TypeError raises a "bad argument" error reporting that argument n was the
// wrong type, naming the expected type (luaL_typeerror).
func (L *LState) TypeError(n int, want string) { L.typeArgError(n, want) }
