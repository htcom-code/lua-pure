// sandbox runs untrusted-ish code safely: a state with only safe libraries, a
// per-call _ENV that confines globals, and a deadline that stops a runaway loop.
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	luapure "github.com/htcom-code/lua-pure/lua"
)

func main() {
	// NewSandbox opens only base/string/table/math/utf8/coroutine; io, os, debug
	// and load/loadfile/dofile are not available.
	L := luapure.NewSandbox()
	if L.GetGlobal("os").IsNil() {
		fmt.Println("os library not exposed in the sandbox")
	}

	// Per-call _ENV isolation: each RunWith gets a fresh environment, so a global
	// a script writes does not leak to the next run. We seed the env with just
	// the few globals we trust.
	for i := 0; i < 2; i++ {
		env := luapure.NewTable()
		env.SetStr("tostring", L.GetGlobal("tostring"))
		res, err := L.RunWith(env, `x = (x or 0) + 1; return x`, "=call")
		if err != nil {
			panic(err)
		}
		fmt.Printf("run %d -> %d (always 1: globals confined)\n", i+1, res[0].AsInt())
	}

	// A deadline stops even a tight infinite loop (checked between instructions).
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	L.SetContext(ctx)
	start := time.Now()
	_, err := L.DoString(`while true do end`, "=runaway")
	L.SetContext(nil)
	fmt.Printf("runaway loop stopped after %v (cancelled=%v)\n",
		time.Since(start).Round(time.Millisecond), err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded))
}
