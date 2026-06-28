// basic is the README quickstart as a runnable program: embed the VM, expose a
// Go function, run a script, and read the results back.
package main

import (
	"fmt"

	luapure "github.com/htcom-code/lua-pure/lua"
)

func main() {
	L := luapure.NewState()
	L.OpenLibs()

	// Expose a Go function to Lua.
	L.Register("greet", func(L *luapure.LState) int {
		who := L.CheckString(1)
		L.Push(luapure.MkString("hello, " + who))
		return 1
	})

	res, err := L.DoString(`return greet("world"), 6 * 7`, "=quickstart")
	if err != nil {
		panic(err)
	}
	fmt.Println(res[0].Str(), res[1].AsInt()) // hello, world 42
}
