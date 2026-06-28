// luadbg-mcp is a Model Context Protocol (MCP) debug server for the luapure
// Lua VM: it lets an LLM agent set breakpoints, step, inspect frames and
// evaluate expressions in a running Lua program through MCP tool calls. It
// speaks MCP over stdio (newline-delimited JSON-RPC) with no external
// dependencies.
//
//	luadbg-mcp                       # programs are launched with inline source
//	luadbg-mcp -source-dir ./scripts # resolve a program id to scripts/<id>[.lua]
//	luadbg-mcp -sandbox              # run debuggees without io/os/load
//
// An MCP client (e.g. Claude Desktop/Code) spawns the binary and drives it. The
// debuggee's source need not live with the client: launch a program by id and
// the server loads it (here from -source-dir; embed the debugmcp package with
// your own SourceResolver to load from a database). stdout carries the protocol
// only — diagnostics go to stderr.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/htcom-code/lua-pure/debugmcp"
	luapure "github.com/htcom-code/lua-pure/lua"
)

func main() {
	var (
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
			// Try the id verbatim, then with a .lua suffix, under the source dir.
			for _, name := range []string{id, id + ".lua"} {
				if b, err := os.ReadFile(filepath.Join(dir, filepath.Clean(name))); err == nil {
					return string(b), true
				}
			}
			return "", false
		}
	}

	srv := &debugmcp.Server{
		NewState: newState,
		Source:   resolver,
		Name:     "luapure-debug",
		Version:  "0.1.0",
	}

	t := debugmcp.NewStdioTransport(os.Stdin, os.Stdout, nil)
	if err := srv.Serve(t); err != nil {
		fmt.Fprintln(os.Stderr, "luadbg-mcp:", err)
		os.Exit(1)
	}
}
