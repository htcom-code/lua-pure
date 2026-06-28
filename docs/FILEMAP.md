# Engine file map (`lua/`)

The engine is one Go package (`package luapure`) in `lua/`. Go requires a
package to live in a single directory, so — like PUC's flat `src/` — the files
are not foldered; instead the `l`-prefix mirrors PUC's source names and the
groups below give a functional index. To find the Go file for a PUC source, the
rule of thumb is `lfoo.c` → `lfoo.go` (split files keep the parent name plus a
suffix). See the README's "File map" table for the full PUC↔Go correspondence.

## objects — values, tables, functions, metamethods
| file | role |
|---|---|
| `lobject.go` | the tagged `Value` core, type tags, comparisons |
| `lobject_gc.go` | GC-object constructors/accessors (table/closure/userdata/thread), type names |
| `lobject_num.go` | number ⇄ string conversions, integer/float coercions |
| `lobject_proto.go` | `Proto` (compiled function): code, constants, line info |
| `ltable.go` | tables (array part + Go map), `__mode` weak handling |
| `lfunc.go` | closures and upvalues |
| `ltm.go` | tag methods / metamethods (`luaT_*`) |

## frontend — lexer, parser, code generator, bytecode
| file | role |
|---|---|
| `llex.go` | lexer (+ folded `lctype` character classes) |
| `lparser.go` | single-pass recursive-descent parser |
| `lparser_codegen.go` | parser↔codegen glue (scopes, registers) |
| `lcode.go` | bytecode emit, constants, jump patching |
| `lopcodes.go` | opcode formats, encode/decode |
| `ldump.go` | precompiled-chunk dump **and** undump (PUC `ldump.c`+`lundump.c`) |
| `lzio.go` | buffered input stream (ZIO) unifying string/file/reader loads |

## runtime — VM, call machinery, state, GC, debug hooks
| file | role |
|---|---|
| `lvm.go` | VM helpers (concat, index, compare) |
| `lvm_execute.go` | the instruction dispatch loop |
| `lvm_arith.go` | raw arithmetic/bitwise ops |
| `ldo.go` | call/return/pcall stack machinery |
| `ldo_hook.go` | debug-hook firing (line/call/return/count) |
| `lstate.go` | `LState` (thread + global state), `callInfo` |
| `ldebug.go` | symbolic names for errors (`getobjname`) |
| `lgc.go` | `__gc` finalizers (delegated to the Go runtime) |
| `lgc_weak.go`, `lgc_weakkey.go` | weak tables (`weak.Pointer`), ephemeron keys |

## stdlib — standard libraries
| file | role |
|---|---|
| `lbaselib.go` | base (`print`, `pcall`, `load`, `setmetatable`, …) |
| `lauxlib.go` | `luaL_*` helpers, arg checking, traceback naming |
| `lstrlib.go`, `lstrlib_pattern.go`, `lstrlib_pack.go` | string library; patterns; `pack`/`unpack` |
| `lmathlib.go` | math (xoshiro256** PRNG) |
| `ltablib.go` | table library |
| `loslib.go` | os library |
| `liolib.go` | io library |
| `lcorolib.go` | coroutine library (goroutine-per-coroutine) |
| `lutf8lib.go` | utf8 library |
| `loadlib.go` | `require` / `package` |
| `ldblib.go` | debug library (`getinfo`, `sethook`, `getlocal`, …) |
| `linit.go` | `OpenLibs` — standard library installation |

## api — the embedding surface (Go ↔ Lua)
| file | role |
|---|---|
| `lapi.go` | compile/load/call entry points (`CompileString`, `DoString`, `CallProto`) |
| `lapi_check.go` | `luaL_check*`/`opt*` argument helpers for Go callbacks |
| `lapi_table.go` | table create/read/write from Go |
| `lapi_load.go` | `CompileReader`, file loading |
| `lapi_sandbox.go` | `NewSandbox`, `RunWith`, `SetContext` |
| `lapi_userdata.go` | full userdata + metatables + uservalues (bind a Go type) |
| `lapi_debug.go` | Go-native debug hook (`SetGoHook`) + `Frame` inspection + `Frame.Eval` |
| `convert.go` | `ToValue` / `FromValue` Go⇄Lua conversion |

## debugger — breakpoint/step controller and front-end facade
| file | role |
|---|---|
| `debug_session.go` | `Debugger`: breakpoints, step into/over/out, pause, `OnStop` |
| `debug_session_driver.go` | `Session`: synchronous facade + `SourceResolver` (source-on-server) for MCP/DAP front ends |

## tooling / config
| file | role |
|---|---|
| `disasm.go` | bytecode disassembler (PUC ships this in `luac`) |
| `luaconf.go` | limits/macros (`luaconf.h`) |
| `doc.go` | package documentation |

## tests
Test files sit beside their subject (`*_test.go`); Go requires a package's
tests to live in its own directory. Unit tests that exercise engine internals
use `package luapure` and stay here in `lua/`; black-box tests and runnable
examples use `package luapure_test`. The conformance suite is public-API-only,
so it lives in its own `conformance/` package (not in `lua/`), alongside the
`cmd/conformance` driver. The official fixtures are at repo root in
`_lua5.4-tests/` (reached from a sub-package via `../_lua5.4-tests/`).
