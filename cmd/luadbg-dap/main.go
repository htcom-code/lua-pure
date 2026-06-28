// luadbg-dap is a Debug Adapter Protocol (DAP) server for the luapure Lua VM,
// served over TCP. An editor (e.g. VS Code with an attach configuration
// pointing at host:port) connects and drives breakpoints, stepping, stack and
// variable inspection, and expression evaluation.
//
//	luadbg-dap -listen :4711 -source-dir ./scripts
//	luadbg-dap -listen 127.0.0.1:4711 -sandbox
//
// A program is launched by id (the DAP launch 'program' argument) and its
// source is loaded from -source-dir/<id>[.lua]; the editor fetches source text
// over DAP 'source' requests, so it needs no local copy. Embed the debugdap
// package with your own SourceResolver to load from a database instead.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	luapure "github.com/htcom-code/lua-pure"
	"github.com/htcom-code/lua-pure/debugdap"
)

func main() {
	var (
		listen    = flag.String("listen", ":4711", "TCP address to serve DAP on")
		sourceDir = flag.String("source-dir", "", "directory to resolve a program id to <dir>/<id>[.lua]")
		sandbox   = flag.Bool("sandbox", false, "run debuggees in a sandbox (no io/os/load)")
	)
	flag.Parse()

	newState := func() *luapure.LState {
		L := luapure.NewState()
		L.OpenLibs()
		return L
	}
	if *sandbox {
		newState = func() *luapure.LState { return luapure.NewSandbox() }
	}

	var resolver luapure.SourceResolver
	if *sourceDir != "" {
		dir := *sourceDir
		resolver = func(id string) (string, bool) {
			for _, name := range []string{id, id + ".lua"} {
				if b, err := os.ReadFile(filepath.Join(dir, filepath.Clean(name))); err == nil {
					return string(b), true
				}
			}
			return "", false
		}
	}

	cfg := debugdap.Config{NewState: newState, Source: resolver}
	fmt.Fprintf(os.Stderr, "luadbg-dap: listening on %s\n", *listen)
	if err := debugdap.ListenAndServe(*listen, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "luadbg-dap:", err)
		os.Exit(1)
	}
}
