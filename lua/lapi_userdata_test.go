package luapure_test

import (
	"strings"
	"testing"

	luapure "github.com/htcom-code/lua-pure/lua"
)

type point struct{ x, y int64 }

// installPoint registers a "Point" metatable with a getX method and a __gc hook,
// returning the state so tests can mint Point userdata against it.
func installPoint(t *testing.T) *luapure.LState {
	t.Helper()
	L := luapure.NewState()
	L.OpenLibs()

	mt, created := L.NewMetatable("Point")
	if !created {
		t.Fatal("NewMetatable should report created on first call")
	}
	if mt2, created2 := L.NewMetatable("Point"); mt2 != mt || created2 {
		t.Fatal("NewMetatable should return the same table and created=false on reuse")
	}

	methods := luapure.NewTable()
	methods.SetStr("getX", luapure.NewGoFunc("getX", func(L *luapure.LState) int {
		p := L.CheckUserData(1, "Point").(*point)
		L.Push(luapure.Int(p.x))
		return 1
	}))
	mt.SetStr("__index", methods.Value())
	return L
}

func TestUserDataRoundTrip(t *testing.T) {
	L := installPoint(t)
	mt := L.GetMetatable("Point")
	if mt == nil {
		t.Fatal("GetMetatable returned nil for a registered name")
	}

	p := &point{x: 3, y: 4}
	ud := L.NewUserData(p, mt)

	if got := ud.AsUserData(); got != any(p) {
		t.Fatalf("AsUserData = %v, want the original *point", got)
	}
	if ud.UserMetatable() != mt {
		t.Fatal("UserMetatable did not return the metatable passed to NewUserData")
	}

	L.SetGlobal("pt", ud)
	res, err := L.DoString(`return pt:getX()`, "=test")
	if err != nil {
		t.Fatalf("calling method through __index: %v", err)
	}
	if got := res[0].AsInt(); got != 3 {
		t.Fatalf("pt:getX() = %d, want 3", got)
	}
}

func TestCheckUserDataWrongType(t *testing.T) {
	L := installPoint(t)
	L.SetGlobal("pt", L.NewUserData(&point{x: 1}, L.GetMetatable("Point")))

	// Passing a plain table where a Point is expected must raise a bad-argument
	// type error naming "Point".
	_, err := L.DoString(`return pt.getX({})`, "=test")
	if err == nil {
		t.Fatal("expected a type error from CheckUserData")
	}
	if !strings.Contains(err.Error(), "Point expected") {
		t.Fatalf("error %q should mention 'Point expected'", err.Error())
	}
}

func TestTestUserData(t *testing.T) {
	L := installPoint(t)
	other, _ := L.NewMetatable("Other")

	p := &point{x: 9}
	L.Register("probe", func(L *luapure.LState) int {
		// Matching name returns the payload; mismatched name returns nil.
		if d := L.TestUserData(1, "Point"); d == nil || d.(*point) != p {
			t.Error("TestUserData should return the payload for a matching type")
		}
		if d := L.TestUserData(1, "Other"); d != nil {
			t.Error("TestUserData should return nil for a mismatched type")
		}
		return 0
	})
	_ = other

	L.SetGlobal("pt", L.NewUserData(p, L.GetMetatable("Point")))
	if _, err := L.DoString(`probe(pt)`, "=test"); err != nil {
		t.Fatalf("probe: %v", err)
	}
}

func TestSetUserMetatable(t *testing.T) {
	L := installPoint(t)
	ud := L.NewUserData(&point{x: 7}, nil)
	if ud.UserMetatable() != nil {
		t.Fatal("userdata created with nil metatable should have none")
	}

	L.SetUserMetatable(ud, L.GetMetatable("Point"))
	L.SetGlobal("pt", ud)
	res, err := L.DoString(`return pt:getX()`, "=test")
	if err != nil {
		t.Fatalf("after SetUserMetatable: %v", err)
	}
	if got := res[0].AsInt(); got != 7 {
		t.Fatalf("pt:getX() = %d, want 7", got)
	}
}

func TestUserValues(t *testing.T) {
	L := installPoint(t)
	ud := L.NewUserDataUV(&point{x: 1}, 2, L.GetMetatable("Point"))

	if got := ud.NumUserValues(); got != 2 {
		t.Fatalf("NumUserValues = %d, want 2", got)
	}
	// Slots start nil.
	if v, ok := ud.UserValue(1); !ok || !v.IsNil() {
		t.Fatalf("fresh slot 1 = (%v, %v), want (nil, true)", v, ok)
	}

	if !ud.SetUserValue(2, luapure.MkString("tag")) {
		t.Fatal("SetUserValue on an in-range slot should succeed")
	}
	if v, ok := ud.UserValue(2); !ok || v.Str() != "tag" {
		t.Fatalf("slot 2 = (%v, %v), want (\"tag\", true)", v, ok)
	}

	// Out-of-range access fails without panicking, both directions.
	if _, ok := ud.UserValue(3); ok {
		t.Error("UserValue(3) on a 2-slot userdatum should report ok=false")
	}
	if ud.SetUserValue(0, luapure.True) {
		t.Error("SetUserValue(0) should report false")
	}
}

// The Go uservalue API and the script-facing debug.getuservalue/setuservalue
// must see the same slots.
func TestUserValuesDebugInterop(t *testing.T) {
	L := installPoint(t)
	ud := L.NewUserDataUV(&point{}, 1, L.GetMetatable("Point"))
	ud.SetUserValue(1, luapure.Int(99))
	L.SetGlobal("u", ud)

	res, err := L.DoString(`
		local v, ok = debug.getuservalue(u, 1)
		debug.setuservalue(u, "changed", 1)
		return v, ok`, "=test")
	if err != nil {
		t.Fatalf("debug uservalue round-trip: %v", err)
	}
	if res[0].AsInt() != 99 || !res[1].AsBool() {
		t.Fatalf("getuservalue saw (%v, %v), want (99, true)", res[0], res[1])
	}
	// The write from Lua is visible to Go.
	if v, _ := ud.UserValue(1); v.Str() != "changed" {
		t.Fatalf("after setuservalue, Go sees %v, want \"changed\"", v)
	}
}

// NewUserData (the no-slot form) yields zero uservalues.
func TestNewUserDataHasNoUserValues(t *testing.T) {
	L := installPoint(t)
	ud := L.NewUserData(&point{}, L.GetMetatable("Point"))
	if got := ud.NumUserValues(); got != 0 {
		t.Fatalf("NewUserData NumUserValues = %d, want 0", got)
	}
	if _, ok := ud.UserValue(1); ok {
		t.Error("a no-slot userdatum should have no readable uservalue")
	}
}

// AsUserData and UserMetatable must be safe (and nil) on non-userdata values.
func TestUserDataAccessorsOnNonUserData(t *testing.T) {
	if luapure.Int(5).AsUserData() != nil {
		t.Error("AsUserData on an integer should be nil")
	}
	if luapure.NewTable().Value().UserMetatable() != nil {
		t.Error("UserMetatable on a table should be nil")
	}
}
