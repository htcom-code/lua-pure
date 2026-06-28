package luapure_test

import (
	"errors"
	"strings"
	"testing"

	luapure "github.com/htcom-code/lua-pure/lua"
)

// RaiseError raises a formatted, position-prefixed error a script's pcall
// catches as a normal message (PUC luaL_error).
func TestRaiseError(t *testing.T) {
	L := luapure.NewState()
	L.OpenLibs()
	L.Register("boom", func(L *luapure.LState) int {
		return L.RaiseError("bad value: %d", 7)
	})

	// Unprotected: surfaces as a *LuaError carrying "src:line: bad value: 7".
	_, err := L.DoString(`return boom()`, "=t")
	var le *luapure.LuaError
	if !errors.As(err, &le) {
		t.Fatalf("want *LuaError, got %T: %v", err, err)
	}
	if !strings.Contains(le.Error(), "bad value: 7") {
		t.Fatalf("message lost: %q", le.Error())
	}
	if !strings.Contains(le.Error(), "t:1:") { // position prefix (chunk "t", line 1)
		t.Fatalf("want position prefix, got %q", le.Error())
	}

	// Protected by the script: pcall returns false + the message.
	res, err := L.DoString(`return pcall(boom)`, "=t2")
	if err != nil {
		t.Fatal(err)
	}
	if res[0].AsBool() || !strings.Contains(res[1].Str(), "bad value: 7") {
		t.Fatalf("pcall result: ok=%v msg=%q", res[0].AsBool(), res[1].Str())
	}
}

// RaiseValue raises an arbitrary value (here a table) as the error object, which
// a script's pcall receives intact (PUC lua_error).
func TestRaiseValue(t *testing.T) {
	L := luapure.NewState()
	L.OpenLibs()
	L.Register("fail", func(L *luapure.LState) int {
		e := luapure.NewTable()
		e.SetStr("code", luapure.Int(42))
		e.SetStr("msg", luapure.MkString("nope"))
		return L.RaiseValue(e.Value())
	})

	// The script sees the raised table and reads its fields.
	res, err := L.DoString(`local ok, e = pcall(fail); return ok, e.code, e.msg`, "=t")
	if err != nil {
		t.Fatal(err)
	}
	if res[0].AsBool() {
		t.Fatal("pcall should report failure")
	}
	if res[1].AsInt() != 42 || res[2].Str() != "nope" {
		t.Fatalf("raised table fields: code=%d msg=%q", res[1].AsInt(), res[2].Str())
	}

	// Unprotected: the host gets the table back via LuaError.Value().
	_, err = L.DoString(`return fail()`, "=t2")
	var le *luapure.LuaError
	if !errors.As(err, &le) {
		t.Fatalf("want *LuaError, got %v", err)
	}
	if !le.Value().IsTable() || le.Value().AsTable().GetStr("code").AsInt() != 42 {
		t.Fatal("LuaError.Value() should be the raised table with code=42")
	}
}
