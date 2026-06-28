// debugger drives the in-process Debugger: set a breakpoint, run a script, and
// each time it stops inspect the frame and evaluate expressions in its scope,
// then resume. OnStop runs synchronously on the VM goroutine, so no transport is
// needed (the debugmcp/debugdap packages wrap this same Debugger for MCP/DAP).
package main

import (
	"fmt"

	luapure "github.com/htcom-code/lua-pure/lua"
)

// Line 8 (out[#out + 1] = classify(i)) is the breakpoint; the loop hits it for
// i = 5, 10, 15.
const src = `local function classify(n)
	local label = "small"
	if n >= 10 then label = "big" end
	return label
end
local out = {}
for i = 5, 15, 5 do
	out[#out + 1] = classify(i)
end
return table.concat(out, ",")
`

func main() {
	L := luapure.NewState()
	L.OpenLibs()

	d := luapure.NewDebugger(L)
	d.SetBreakpoints("demo", []int{8}) // matches the "=demo" chunk's short source

	hits := 0
	d.OnStop(func(d *luapure.Debugger, reason luapure.StopReason) {
		hits++
		top := d.Frames()[0]
		// Evaluate expressions in the stopped frame's scope — read a local and
		// even call a local function.
		i, _ := top.Eval("i")
		label, _ := top.Eval("classify(i)")
		fmt.Printf("stop #%d at %s:%d  i=%d  classify(i)=%q\n",
			hits, top.ShortSource(), top.CurrentLine(), i[0].AsInt(), label[0].Str())
		d.Continue() // or StepInto / StepOver / StepOut
	})

	res, err := L.DoString(src, "=demo")
	if err != nil {
		panic(err)
	}
	fmt.Println("result:", res[0].Str())
}
