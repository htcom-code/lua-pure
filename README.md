# LuaPure — a pure Go Lua VM

[![Go Reference](https://pkg.go.dev/badge/github.com/htcom-code/lua-pure/lua.svg)](https://pkg.go.dev/github.com/htcom-code/lua-pure/lua)
[![Go 1.24+](https://img.shields.io/badge/Go-1.24%2B-00ADD8.svg)](go.mod)
[![Lua 5.4.8](https://img.shields.io/badge/Lua-5.4.8-000080.svg)](https://www.lua.org/)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Status: alpha](https://img.shields.io/badge/status-alpha-orange.svg)](#project-status)

`luapure` is a pure-Go implementation of **PUC-Lua 5.4** — no cgo, no bundled C.
Its instruction set, single-pass compiler, virtual machine, standard libraries,
and semantics are ported directly from the reference C sources (`lua-5.4.8/src`),
written from scratch against them, sharing no code with any other engine. The
standard-library surface and observable behaviour match a stock PUC-Lua 5.4.8
build, gated by the official Lua 5.4 test suite.

## Features

- **Pure Go, zero cgo** — `go build` cross-compiles anywhere Go does; no C
  toolchain, no `lua.h`, no linker flags.
- **Faithful PUC-Lua 5.4.8** — same bytecode, error messages, and edge cases;
  loads/emits `luac 5.4.8` binary chunks byte-for-byte on a 64-bit LE host.
- **Complete 5.4 standard library** — `base`, `package`, `string`, `table`,
  `math`, `os`, `io`, `coroutine`, `utf8`, `debug`.
- **Go-native embedding surface PUC lacks** — per-`State` functional options,
  `context.Context` cancellation, an instruction budget, configurable memory
  caps, host-module registration, and userdata.
- **Sandboxing** — confine untrusted scripts to safe libraries with a single
  option (`WithSandbox`); see [`SECURITY.md`](SECURITY.md).
- **Built-in debugger** — in-process breakpoints/stepping plus standalone debug
  servers over MCP (`debugmcp/`) and DAP (`debugdap/`).

## Why luapure

| library | Lua version | cgo / C toolchain |
|---|---|---|
| [gopher-lua](https://github.com/yuin/gopher-lua) | 5.1 | no |
| [Shopify/go-lua](https://github.com/Shopify/go-lua) | 5.2 | no |
| cgo bindings (golua, etc.) | real Lua 5.x | **yes** |
| **luapure** | **5.4.8** | **no** |

If you want current Lua (5.4) semantics *and* a single static Go binary with no
C toolchain, luapure is the gap the table above shows.

## Performance

Pure Go costs roughly **1.3–1.6× PUC on table/string work and ~2.45× on a tight
arithmetic loop** — measured back-to-back against the C interpreter, with no
cgo. See [`docs/BENCHMARKS.md`](docs/BENCHMARKS.md) for the full cross-engine
numbers (PUC vs cgo bindings vs luapure) and methodology.

## Project status

Pre-1.0 (`v0.1.0`, alpha). The Lua language surface tracks PUC-Lua 5.4.8 and is
stable; the **Go embedding API may still change** before a 1.0 tag. Pin a commit
or tag if you depend on it. [`ROADMAP.md`](ROADMAP.md) tracks what comes next
(PUC-Lua 5.5).

## Install

```sh
go get github.com/htcom-code/lua-pure/lua
```

Requires Go 1.24 (uses the `weak` package for weak tables).

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

Standalone runnable programs are in [`examples/`](examples/) (`go run
./examples/<name>`); smaller doc-integrated snippets are the `Example` functions
in [`lua/example_test.go`](lua/example_test.go), which render on pkg.go.dev.

## Concurrency

An `LState` is a **single execution context**: drive each one from a single
goroutine at a time, and never share a *running* state across goroutines.
Coroutines are internal goroutines that hand off over channels, so exactly one
runs at any instant — there is no parallelism *inside* a state to exploit, and
concurrent external access would race.

`NewState` itself **is safe to call concurrently** (verified under `-race`), so
the idiomatic way to use many cores is a **pool of states** — one per worker
goroutine — rather than one shared state. The exported version identifiers
`luapure.Version` / `luapure.VersionString()` (and the Lua globals `_VERSION`,
`_LUAPURE_VERSION`) report the engine build.

## Repository layout

The engine is a single Go package (one package = one directory, so it stays
flat like PUC's `src/`; navigate it by [`docs/FILEMAP.md`](docs/FILEMAP.md),
which carries the full PUC source → Go file map and a functional index). Files
keep PUC's `l`-prefixed names; where one PUC source is split across several Go
files (Go favours smaller focused files), the parts share the PUC parent name
with a suffix. Add-ons that build on its public API are separate packages.

| path | package | what |
|---|---|---|
| `lua/` | `luapure` — import `github.com/htcom-code/lua-pure/lua` | the engine: VM, compiler, libraries, embedding API |
| `debugmcp/` | `debugmcp` | debug server over the Model Context Protocol (stdio + HTTP) |
| `debugdap/` | `debugdap` | debug server over the Debug Adapter Protocol (TCP) |
| `conformance/` | `conformance` (tests) | black-box `go test` suite: curated 5.4 snippets + the PUC behaviour-probe ext suite |
| `cmd/conformance/` | `main` | standalone driver that runs the official `_lua5.4-tests/` fixtures |
| `cmd/luadbg-mcp/`, `cmd/luadbg-dap/` | `main` | standalone debug-server binaries |
| `examples/` | `main` (one dir each) | runnable embedding-API examples ([`examples/README.md`](examples/README.md)) |
| `_lua5.4-tests/` | — | the official Lua 5.4 test suite (fixtures) |
| `_glue5.4-tests/` | — | extra self-asserting probes pinned to PUC 5.4 oracle values, run by the ext suite |

## Differences from PUC

The standard-library surface and observable semantics match a stock PUC-Lua
5.4.8 build. The deliberate divergences are structural — GC delegated to the Go
runtime, a tagged-struct `Value` (not NaN-boxing), `panic`/`recover` error
unwinding, goroutine coroutines, and configurable memory-limit caps — and a few
host-dependent gaps (`io.popen`, dynamic C library loading). luapure also adds a
Go-native embedding surface PUC lacks (per-State options, context cancellation,
instruction budget, sandboxing, host-module registration, a debugger).

[`docs/COMPATIBILITY.md`](docs/COMPATIBILITY.md) is the full, categorized list.

## Documentation

- [`docs/COMPATIBILITY.md`](docs/COMPATIBILITY.md) — how it differs from PUC
- [`docs/BENCHMARKS.md`](docs/BENCHMARKS.md) — cross-engine speed
- [`docs/FILEMAP.md`](docs/FILEMAP.md) — PUC source → Go file map
- [`ROADMAP.md`](ROADMAP.md) — what tracks next (PUC-Lua 5.5)
- [`SECURITY.md`](SECURITY.md) — sandboxing and the threat model
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — the port discipline and test gate
- [`CHANGELOG.md`](CHANGELOG.md) — release history
- [pkg.go.dev](https://pkg.go.dev/github.com/htcom-code/lua-pure/lua) — API
  reference (or `make doc-web` to serve it locally; `make doc` for text)

The conformance harness is `go run ./cmd/conformance` (runs the official Lua 5.4
test suite in `_lua5.4-tests/`).

## License & attribution

luapure is a derivative work — a Go port of **PUC-Lua 5.4**. The language,
instruction set, compiler, VM, libraries, and semantics are PUC-Rio's; this code
translates them to Go and adds the Go-native adaptations listed above. Lua is
MIT, as is this project; its copyright notice is retained.

- **Lua**: Copyright © 1994–2025 Lua.org, PUC-Rio (MIT) — https://www.lua.org/
  — full notice in [`LICENSE-Lua`](LICENSE-Lua) (the engine is ported from it).
- **luapure**: the Go port and Go-native adaptations, MIT — see [`LICENSE`](LICENSE).

Both are MIT and compatible.
