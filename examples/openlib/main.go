// openlib shows how to write your own library that installs like a built-in
// one. The standard libraries register via methods on the state (L.OpenString,
// L.OpenMath, …); from an embedding package you write the exact same pattern as
// a free function taking *LState — build a module table, fill it with Go
// functions and constants, and install it as a global. (For `require("x")`
// integration instead, see the customlib example's Requiref/Preload.)
//
// The payoff: a custom library is how you expose Go's standard library — here a
// few strings helpers Lua's string library does not have.
package main

import (
	"fmt"
	"strings"

	luapure "github.com/htcom-code/lua-pure/lua"
)

// OpenStrExtra installs the `strextra` library, mirroring how OpenString works
// internally (newTable + register funcs + set as a global).
func OpenStrExtra(L *luapure.LState) {
	t := luapure.NewTable()
	t.SetStr("startswith", luapure.NewGoFunc("startswith", func(L *luapure.LState) int {
		L.Push(luapure.Bool(strings.HasPrefix(L.CheckString(1), L.CheckString(2))))
		return 1
	}))
	t.SetStr("endswith", luapure.NewGoFunc("endswith", func(L *luapure.LState) int {
		L.Push(luapure.Bool(strings.HasSuffix(L.CheckString(1), L.CheckString(2))))
		return 1
	}))
	t.SetStr("split", luapure.NewGoFunc("split", func(L *luapure.LState) int {
		parts := strings.Split(L.CheckString(1), L.CheckString(2))
		out := luapure.NewTable()
		for i, p := range parts {
			out.SetInt(int64(i+1), luapure.MkString(p))
		}
		L.Push(out.Value())
		return 1
	}))
	t.SetStr("VERSION", luapure.MkString("1.0"))
	L.SetGlobal("strextra", t.Value())
}

func main() {
	L := luapure.NewState(luapure.WithOpenLibs())
	OpenStrExtra(L) // called just like a built-in opener

	res, err := L.DoString(`
		local parts = strextra.split("a,b,c", ",")
		return strextra.startswith("hello", "he"), #parts, parts[2], strextra.VERSION
	`, "=demo")
	if err != nil {
		panic(err)
	}
	fmt.Println(res[0].AsBool(), res[1].AsInt(), res[2].Str(), res[3].Str())
	// true 3 b 1.0
}
