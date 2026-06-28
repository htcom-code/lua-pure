package luapure_test

import (
	"testing"

	luapure "github.com/htcom-code/lua-pure/lua"
)

// openMy returns a module opener that bumps *calls each time it runs, so tests
// can assert open-once / lazy behaviour.
func openMy(calls *int) func(*luapure.LState) *luapure.Table {
	return func(L *luapure.LState) *luapure.Table {
		*calls++
		t := luapure.NewTable()
		t.SetStr("tag", luapure.MkString("v1"))
		t.SetStr("greet", luapure.NewGoFunc("greet", func(L *luapure.LState) int {
			L.Push(luapure.MkString("hi " + L.CheckString(1)))
			return 1
		}))
		return t
	}
}

// Requiref(glb=true): installs a global AND makes require(name) return the same
// module, calling the opener exactly once.
func TestRequirefGlobalAndRequire(t *testing.T) {
	L := luapure.NewState(luapure.WithOpenLibs())
	calls := 0
	if L.Requiref("mylib", openMy(&calls), true) == nil {
		t.Fatal("nil module")
	}
	if calls != 1 {
		t.Fatalf("opener called %d times, want 1", calls)
	}
	res, err := L.DoString(`return mylib.greet("world")`, "=g")
	if err != nil || res[0].Str() != "hi world" {
		t.Fatalf("global use: %v %v", err, res)
	}
	res, err = L.DoString(`return require("mylib").tag`, "=r")
	if err != nil || res[0].Str() != "v1" {
		t.Fatalf("require: %v %v", err, res)
	}
	if calls != 1 {
		t.Fatalf("require re-opened the module: calls=%d", calls)
	}
}

// Requiref is idempotent: a second call returns the cached module, no re-open.
func TestRequirefIdempotent(t *testing.T) {
	L := luapure.NewState(luapure.WithOpenLibs())
	calls := 0
	L.Requiref("m", openMy(&calls), false)
	L.Requiref("m", openMy(&calls), false)
	if calls != 1 {
		t.Fatalf("opener called %d times, want 1 (idempotent)", calls)
	}
}

// Requiref(glb=false): not a global, but require(name) still resolves it.
func TestRequirefNoGlobal(t *testing.T) {
	L := luapure.NewState(luapure.WithOpenLibs())
	calls := 0
	L.Requiref("m", openMy(&calls), false)

	res, _ := L.DoString(`return m`, "=ng")
	if !res[0].IsNil() {
		t.Fatal("module must not be global when glb=false")
	}
	res, err := L.DoString(`return require("m").tag`, "=r")
	if err != nil || res[0].Str() != "v1" {
		t.Fatalf("require: %v %v", err, res)
	}
}

// Preload is lazy: the opener runs only on the first require, then the module is
// cached (no re-open on a second require).
func TestPreloadLazy(t *testing.T) {
	L := luapure.NewState(luapure.WithOpenLibs())
	calls := 0
	L.Preload("lazy", openMy(&calls))
	if calls != 0 {
		t.Fatal("Preload must not open the module eagerly")
	}
	res, err := L.DoString(`return require("lazy").tag`, "=r")
	if err != nil || res[0].Str() != "v1" {
		t.Fatalf("require: %v %v", err, res)
	}
	if calls != 1 {
		t.Fatalf("opener called %d times, want 1", calls)
	}
	if _, err := L.DoString(`return require("lazy")`, "=r2"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("second require re-opened the module: calls=%d", calls)
	}
}
