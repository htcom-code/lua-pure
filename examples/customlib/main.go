// customlib registers host-provided modules so scripts can require() them:
// Requiref opens a module eagerly (and optionally as a global), Preload registers
// a lazy loader that runs on the first require.
package main

import (
	"fmt"

	luapure "github.com/htcom-code/lua-pure/lua"
)

func main() {
	L := luapure.NewState()
	L.OpenLibs()

	// Eager: build the module now, install it as a global, and make require resolve it.
	L.Requiref("mathx", func(L *luapure.LState) *luapure.Table {
		t := luapure.NewTable()
		t.SetStr("double", luapure.NewGoFunc("double", func(L *luapure.LState) int {
			L.Push(luapure.Int(L.CheckInt(1) * 2))
			return 1
		}))
		return t
	}, true)

	res, err := L.DoString(`return mathx.double(21), require("mathx").double(10)`, "=eager")
	if err != nil {
		panic(err)
	}
	fmt.Println(res[0].AsInt(), res[1].AsInt()) // 42 20

	// Lazy: the opener runs only on the first require("greet").
	L.Preload("greet", func(L *luapure.LState) *luapure.Table {
		t := luapure.NewTable()
		t.SetStr("hi", luapure.NewGoFunc("hi", func(L *luapure.LState) int {
			L.Push(luapure.MkString("hi " + L.CheckString(1)))
			return 1
		}))
		return t
	})

	res, err = L.DoString(`return require("greet").hi("world")`, "=lazy")
	if err != nil {
		panic(err)
	}
	fmt.Println(res[0].Str()) // hi world
}
