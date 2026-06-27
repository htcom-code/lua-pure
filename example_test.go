package luapure_test

import (
	"errors"
	"fmt"
	"strings"

	luapure "github.com/htcom-code/lua-pure"
)

// Build a table in Go, hand it to a script as a global, and read the results
// back — the core embedding round-trip.
func Example_tableExchange() {
	L := luapure.NewState()
	L.OpenLibs()

	cfg := luapure.NewTable()
	cfg.SetStr("name", luapure.MkString("luapure"))
	cfg.SetStr("level", luapure.Int(5))
	L.SetGlobal("config", cfg.Value())

	res, err := L.DoString(`return config.name, config.level * 2`, "=embed")
	if err != nil {
		panic(err)
	}
	fmt.Println(res[0].Str(), res[1].AsInt())
	// Output: luapure 10
}

// Register a Go function that validates its arguments with CheckString, exactly
// like the standard library functions do.
func Example_register() {
	L := luapure.NewState()
	L.OpenLibs()

	L.Register("greet", func(L *luapure.LState) int {
		who := L.CheckString(1)
		L.Push(luapure.MkString("hello, " + who))
		return 1
	})

	res, _ := L.DoString(`return greet("world")`, "=embed")
	fmt.Println(res[0].Str())
	// Output: hello, world
}

// A Lua error from a protected Call comes back as a *LuaError carrying the
// raised value.
func ExampleLState_Call() {
	L := luapure.NewState()
	L.OpenLibs()

	res, _ := L.DoString(`return function() error("boom") end`, "=embed")
	fn := res[0]

	_, err := L.Call(fn, nil, 0)
	var le *luapure.LuaError
	fmt.Println(errors.As(err, &le), strings.Contains(le.Error(), "boom"))
	// Output: true true
}

// ToValue converts Go data into Lua tables; AsTable reads them back.
func ExampleLState_ToValue() {
	L := luapure.NewState()
	v := L.ToValue([]any{10, 20, 30})
	t := v.AsTable()
	fmt.Println(t.Len(), t.GetInt(2).AsInt())
	// Output: 3 20
}

// FromValue converts a Lua table to a map[any]any.
func ExampleFromValue() {
	L := luapure.NewState()
	v := L.ToValue(map[string]any{"x": 42})
	m := luapure.FromValue(v).(map[any]any)
	fmt.Println(m["x"])
	// Output: 42
}
