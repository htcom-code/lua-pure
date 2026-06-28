// embed shows the data round-trip: build a table in Go and hand it to a script
// as a global, then convert Go values to Lua and back with ToValue/FromValue.
package main

import (
	"fmt"

	luapure "github.com/htcom-code/lua-pure/lua"
)

func main() {
	L := luapure.NewState()
	L.OpenLibs()

	// A config table built in Go, exposed as a global the script reads.
	cfg := luapure.NewTable()
	cfg.SetStr("name", luapure.MkString("luapure"))
	cfg.SetStr("level", luapure.Int(5))
	L.SetGlobal("config", cfg.Value())

	res, err := L.DoString(`return config.name, config.level * 2`, "=embed")
	if err != nil {
		panic(err)
	}
	fmt.Println(res[0].Str(), res[1].AsInt()) // luapure 10

	// ToValue turns Go data into Lua values; FromValue converts them back.
	list := L.ToValue([]any{10, 20, 30}).AsTable()
	fmt.Println(list.Len(), list.GetInt(2).AsInt()) // 3 20

	m := luapure.FromValue(L.ToValue(map[string]any{"x": 42})).(map[any]any)
	fmt.Println(m["x"]) // 42
}
