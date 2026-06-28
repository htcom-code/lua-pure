package luapure_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	luapure "github.com/htcom-code/lua-pure/lua"
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

// Bind a Go type into Lua as userdata: register a named metatable once, give it
// a method table, then hand scripts a userdata value they call methods on. The
// method recovers the Go value type-checked with CheckUserData.
func Example_userdata() {
	type counter struct{ n int64 }

	L := luapure.NewState()
	L.OpenLibs()

	mt, _ := L.NewMetatable("Counter")
	methods := luapure.NewTable()
	methods.SetStr("inc", luapure.NewGoFunc("inc", func(L *luapure.LState) int {
		c := L.CheckUserData(1, "Counter").(*counter)
		c.n += L.OptInt(2, 1)
		L.Push(luapure.Int(c.n))
		return 1
	}))
	mt.SetStr("__index", methods.Value())

	L.SetGlobal("c", L.NewUserData(&counter{}, mt))

	res, err := L.DoString(`c:inc(); c:inc(10); return c:inc()`, "=embed")
	if err != nil {
		panic(err)
	}
	fmt.Println(res[0].AsInt())
	// Output: 12
}

// A uservalue keeps a Lua value associated with (and reachable from) a
// userdatum. Here a script stashes a label on the object and reads it back; the
// host sees the same slot through UserValue.
func Example_uservalue() {
	L := luapure.NewState()
	L.OpenLibs()

	mt, _ := L.NewMetatable("Box")
	ud := L.NewUserDataUV(struct{}{}, 1, mt)
	L.SetGlobal("box", ud)

	if _, err := L.DoString(`debug.setuservalue(box, "hello", 1)`, "=embed"); err != nil {
		panic(err)
	}
	v, _ := ud.UserValue(1)
	fmt.Println(v.Str())
	// Output: hello
}

// A sandbox state omits the dangerous libraries and the code-loading globals.
func Example_sandbox() {
	L := luapure.NewSandbox()
	res, _ := L.DoString(`return load == nil, io == nil, (1 + 2)`, "=embed")
	fmt.Println(res[0].AsBool(), res[1].AsBool(), res[2].AsInt())
	// Output: true true 3
}

// RunWith confines a chunk to a custom _ENV: it sees only the globals you put
// in env.
func ExampleLState_RunWith() {
	L := luapure.NewState()
	env := luapure.NewTable()
	env.SetStr("x", luapure.Int(21))

	res, err := L.RunWith(env, `return x * 2`, "=embed")
	if err != nil {
		panic(err)
	}
	fmt.Println(res[0].AsInt())
	// Output: 42
}

// A cancelled context interrupts even a tight infinite loop.
func ExampleLState_SetContext() {
	L := luapure.NewState()
	L.OpenLibs()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before we run
	L.SetContext(ctx)

	_, err := L.DoString(`while true do end`, "=embed")
	fmt.Println(err != nil)
	// Output: true
}

// CompileReader compiles source streamed from any io.Reader.
func ExampleCompileReader() {
	L := luapure.NewState()
	L.OpenLibs()

	p, err := luapure.CompileReader(strings.NewReader(`return 6 * 7`), "=embed")
	if err != nil {
		panic(err)
	}
	res, _ := L.CallProto(p, 1)
	fmt.Println(res[0].AsInt())
	// Output: 42
}

// DoFile loads and runs a script from disk.
func ExampleLState_DoFile() {
	L := luapure.NewState()
	L.OpenLibs()

	dir, _ := os.MkdirTemp("", "luapure")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "s.lua")
	if err := os.WriteFile(path, []byte(`return 1 + 2 + 3`), 0o644); err != nil {
		panic(err)
	}

	res, err := L.DoFile(path)
	if err != nil {
		panic(err)
	}
	fmt.Println(res[0].AsInt())
	// Output: 6
}

// Close releases the state's resources and is idempotent.
func ExampleLState_Close() {
	L := luapure.NewState()
	L.OpenLibs()
	L.Close()
	L.Close()
	fmt.Println("ok")
	// Output: ok
}
