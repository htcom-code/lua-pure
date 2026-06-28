// userdata binds a Go type into Lua: register a named metatable, give it a
// method table, and hand scripts a userdatum they call methods on. The method
// recovers the Go value type-checked with CheckUserData.
package main

import (
	"fmt"

	luapure "github.com/htcom-code/lua-pure/lua"
)

type counter struct{ n int64 }

func main() {
	L := luapure.NewState()
	L.OpenLibs()

	mt, _ := L.NewMetatable("Counter")
	methods := luapure.NewTable()
	methods.SetStr("inc", luapure.NewGoFunc("inc", func(L *luapure.LState) int {
		c := L.CheckUserData(1, "Counter").(*counter)
		c.n += L.OptInt(2, 1) // default step 1
		L.Push(luapure.Int(c.n))
		return 1
	}))
	mt.SetStr("__index", methods.Value())

	L.SetGlobal("c", L.NewUserData(&counter{}, mt))

	res, err := L.DoString(`c:inc(); c:inc(10); return c:inc()`, "=userdata")
	if err != nil {
		panic(err)
	}
	fmt.Println(res[0].AsInt()) // 12
}
