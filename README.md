# lua-pure — a pure-Go Lua 5.4 VM

`luapure` is a pure-Go implementation of **PUC-Lua 5.4**: its instruction set,
single-pass compiler, virtual machine, standard libraries, and semantics are
ported directly from the reference C sources (`lua-5.4.8/src`).

The project began as a fork of [gopher-lua](https://github.com/yuin/gopher-lua)
(a Lua 5.1 engine), but gopher-lua's 5.1 bytecode format and design hit a
structural ceiling for 5.4 (LOADKX / 256-register `RK` / `MMBIN`, etc.), so it
was **rewritten from scratch against the PUC 5.4 sources** into this separate
engine. It no longer shares code with the gopher-lua 5.1 engine.

- Front end: single-pass recursive-descent lexer + parser fused with code
  generation (no AST), mirroring PUC's `llex.c`/`lparser.c`/`lcode.c`.
- Conformance harness: `go run ./cmd/conformance` (runs the official Lua 5.4
  test suite in `_lua5.4-tests/`).
- Requires Go 1.24 (uses the `weak` package for weak tables).

Files keep PUC's `l`-prefixed names. Where one PUC source is split across
several Go files (Go favours smaller focused files), the parts share the PUC
parent name with a suffix.

## File map (PUC source → this package)

| PUC source | this package | notes |
|---|---|---|
| `lapi.c` / `lapi.h` | `lapi.go` | compile/load/call entry points (`CompileString`, `DoString`, `CallProto`) |
| `lauxlib.c` / `.h` | `lauxlib.go` | `luaL_*` helpers, arg checking, traceback frame naming |
| `lbaselib.c` | `lbaselib.go` | base library (`print`, `pcall`, `setmetatable`, `load`, …) |
| `lcode.c` / `.h` | `lcode.go` | code generator (bytecode emit, constants, jumps) |
| `lcorolib.c` | `lcorolib.go` | coroutine library (goroutine-per-coroutine model) |
| `lctype.c` / `.h` | *(folded into `llex.go`)* | ASCII character classes (`lisdigit`, …) |
| `ldblib.c` | `ldblib.go` | debug library (`getinfo`, `getlocal`, `sethook`, …) |
| `ldebug.c` / `.h` | `ldebug.go` | symbolic names for errors (`getobjname`); call-name logic also in `lauxlib.go` |
| `ldo.c` / `.h` | `ldo.go`, `ldo_hook.go` | call/return/pcall stack machinery; debug-hook firing |
| `ldump.c` + `lundump.c` / `.h` | `ldump.go` | binary chunk dump **and** undump (combined) |
| `lfunc.c` / `.h` | `lfunc.go` | closures and upvalues |
| `lgc.c` / `.h` | `lgc.go`, `lgc_weak.go` | `__gc` finalizers; `__mode` weak tables. **GC itself is delegated to the Go runtime** (see below) |
| `linit.c` | `linit.go` | `OpenLibs` — standard library installation |
| `liolib.c` | `liolib.go` | io library |
| `llex.c` / `.h` | `llex.go` | lexer |
| `llimits.h` | *(folded into callers)* | limits/macros (`MAXARG_*`, `intop`, …) |
| `lmathlib.c` | `lmathlib.go` | math library (xoshiro256\*\* PRNG, bit-identical to PUC) |
| `lmem.c` / `.h` | *(none — Go GC)* | PUC's manual allocator has no counterpart |
| `loadlib.c` | `loadlib.go` | `load`/`require`/`package` |
| `lobject.c` / `.h` | `lobject.go`, `lobject_gc.go`, `lobject_num.go`, `lobject_proto.go` | `Value` core; GC-object accessors; number⇄string; `Proto` |
| `lopcodes.c` / `.h` | `lopcodes.go` | opcode formats and decode/encode |
| `loslib.c` | `loslib.go` | os library |
| `lparser.c` / `.h` | `lparser.go`, `lparser_codegen.go` | parser; compiler/scope/register glue |
| `lstate.c` / `.h` | `lstate.go` | `LState` (thread + global state fused), `callInfo` |
| `lstring.c` / `.h` | *(folded into `lobject.go`/`lcode.go`)* | per-chunk literal interning instead of a global string table |
| `lstrlib.c` | `lstrlib.go`, `lstrlib_pattern.go`, `lstrlib_pack.go` | string library; pattern matching; `string.pack`/`unpack` |
| `ltable.c` / `.h` | `ltable.go` | tables (split array part + Go map) |
| `ltablib.c` | `ltablib.go` | table library |
| `ltm.c` / `.h` | `ltm.go` | tag methods / metamethods |
| `lutf8lib.c` | `lutf8lib.go` | utf8 library |
| `lvm.c` / `.h` | `lvm.go`, `lvm_execute.go`, `lvm_arith.go` | VM helpers (concat, index, compare); dispatch loop; raw arithmetic |
| `lzio.c` / `.h` | `lzio.go` | buffered input stream (ZIO) — unifies string/file/reader load paths |
| `luac.c` (print) | `disasm.go` | bytecode disassembler (PUC ships this in the `luac` tool, not the library) |
| `lua.c` | *(repo `cmd/`)* | standalone interpreter — not part of this package |

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
- **Binary chunks** use PUC's exact header but a gopher-native body (round-trip
  only; not `luac`-compatible).
- **Memory-limit caps** (configurable `MaxTableArraySize`, `MaxLexElement`,
  constant count): Go cannot turn an allocation failure into a catchable error
  (OOM is a fatal runtime throw), so these size checks stand in for PUC's
  malloc-failure path. Default off; the conformance runner sets them.

## License & attribution

luapure is a derivative work — a Go port of **PUC-Lua 5.4**. The language,
instruction set, compiler, VM, libraries, and semantics are PUC-Rio's; this code
translates them to Go and adds the Go-native adaptations listed above. The
project also originated as a fork of gopher-lua. Both upstreams are MIT, as is
this project; their copyright notices are retained.

- **Lua**: Copyright © 1994–2025 Lua.org, PUC-Rio (MIT) — https://www.lua.org/
  — full notice in [`LICENSE-Lua`](LICENSE-Lua) (the engine is ported from it).
- **gopher-lua**: Copyright © 2015 Yusuke Inuzuka (MIT) —
  https://github.com/yuin/gopher-lua — fork origin; see [`LICENSE`](LICENSE).
- **luapure**: the Go port and Go-native adaptations, MIT — see [`LICENSE`](LICENSE).

All three are MIT and compatible.
