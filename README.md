# LuaPure — a pure Go Lua VM

📖 **Docs:** `make doc-web` serves the API reference locally (pkgsite, the
pkg.go.dev engine); `make doc` prints it as text. The repository is private, so
there is no public pkg.go.dev page yet.

`luapure` is a pure-Go implementation of **PUC-Lua 5.4**: its instruction set,
single-pass compiler, virtual machine, standard libraries, and semantics are
ported directly from the reference C sources (`lua-5.4.8/src`). It is written
from scratch against those sources and shares no code with any other engine.

- Front end: single-pass recursive-descent lexer + parser fused with code
  generation (no AST), mirroring PUC's `llex.c`/`lparser.c`/`lcode.c`.
- Conformance harness: `go run ./cmd/conformance` (runs the official Lua 5.4
  test suite in `_lua5.4-tests/`).
- Requires Go 1.24 (uses the `weak` package for weak tables).

Files keep PUC's `l`-prefixed names. Where one PUC source is split across
several Go files (Go favours smaller focused files), the parts share the PUC
parent name with a suffix.

See [`ROADMAP.md`](ROADMAP.md) for what tracks next (PUC-Lua 5.5),
[`CONTRIBUTING.md`](CONTRIBUTING.md) for the port discipline and test gate, and
[`CHANGELOG.md`](CHANGELOG.md) for release history.

## Install

```sh
go get github.com/htcom-code/lua-pure/lua
```

## Quickstart

Embed the VM, exchange a value with a script, and read the result back:

```go
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
```

`NewState` also takes functional options, so library opening and limits can be
folded into the constructor (no options = the two-step form above):

```go
// Open the standard libraries and cap the value stack for this state only.
L := luapure.NewState(luapure.WithOpenLibs(), luapure.WithMaxStack(50000))

// A confined state for untrusted code (safe libraries only, time-limited).
L := luapure.NewState(luapure.WithSandbox(), luapure.WithContext(ctx))
```

`WithMaxStack`/`WithMaxCCalls`/`WithMaxTableArraySize` override the package
globals (`luaconf.go`) for that state alone; other states keep the process-wide
defaults.

More runnable examples (tables, userdata, sandboxing, structured errors) live in
[`lua/example_test.go`](lua/example_test.go) and render in `make doc-web`.

## Repository layout

The engine is a single Go package (one package = one directory, so it stays
flat like PUC's `src/`; navigate it by [`docs/FILEMAP.md`](docs/FILEMAP.md),
which carries the full PUC source → Go file map and a functional index).
Add-ons that build on its public API are separate packages.

| path | package | what |
|---|---|---|
| `lua/` | `luapure` — import `github.com/htcom-code/lua-pure/lua` | the engine: VM, compiler, libraries, embedding API |
| `debugmcp/` | `debugmcp` | debug server over the Model Context Protocol (stdio + HTTP) |
| `debugdap/` | `debugdap` | debug server over the Debug Adapter Protocol (TCP) |
| `conformance/` | `conformance` (tests) | black-box `go test` suite: curated 5.4 snippets + the PUC behaviour-probe ext suite |
| `cmd/conformance/` | `main` | standalone driver that runs the official `_lua5.4-tests/` fixtures |
| `cmd/luadbg-mcp/`, `cmd/luadbg-dap/` | `main` | standalone debug-server binaries |
| `_lua5.4-tests/` | — | the official Lua 5.4 test suite (fixtures) |
| `_glue5.4-tests/` | — | extra self-asserting probes pinned to PUC 5.4 oracle values, run by the ext suite |

## Intentional divergences from PUC (same observable behavior)

These are deliberate structural choices; observable semantics, error messages,
and edge cases are still matched against the PUC sources.

- **GC** is delegated to the Go runtime (no `lmem`/incremental collector). Weak
  tables use `weak.Pointer` (Go 1.24); `__gc` uses `runtime.SetFinalizer` plus a
  main-thread drain queue. `collectgarbage` drives `runtime.GC()`.
- **`Value`** is a tagged struct (tag + inline scalar + GC pointer), not
  NaN-boxing — Go's precise GC must be able to follow pointers.
- **Errors** unwind via `panic`/`recover` instead of `longjmp`.
- **Coroutines** are goroutines handing off over channels (so a yield can cross
  a Go/"C" frame, e.g. inside a metamethod).
- **Binary chunks** use PUC-Lua 5.4's exact precompiled-chunk format (a port of
  `ldump.c`/`lundump.c`): a dump is byte-identical to `luac 5.4.8` on a 64-bit
  little-endian host, and luapure loads `luac` output and vice versa. (Internally
  luapure keeps absolute line numbers; dump recompresses them to PUC's
  `lineinfo`/`abslineinfo` and undump restores them.)
- **Memory-limit caps** (configurable `MaxTableArraySize`, `MaxLexElement`,
  constant count): Go cannot turn an allocation failure into a catchable error
  (OOM is a fatal runtime throw), so these size checks stand in for PUC's
  malloc-failure path. Default off; the conformance runner sets them.

## License & attribution

luapure is a derivative work — a Go port of **PUC-Lua 5.4**. The language,
instruction set, compiler, VM, libraries, and semantics are PUC-Rio's; this code
translates them to Go and adds the Go-native adaptations listed above. Lua is
MIT, as is this project; its copyright notice is retained.

- **Lua**: Copyright © 1994–2025 Lua.org, PUC-Rio (MIT) — https://www.lua.org/
  — full notice in [`LICENSE-Lua`](LICENSE-Lua) (the engine is ported from it).
- **luapure**: the Go port and Go-native adaptations, MIT — see [`LICENSE`](LICENSE).

All three are MIT and compatible.
