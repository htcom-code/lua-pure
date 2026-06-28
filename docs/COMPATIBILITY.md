# Compatibility with PUC-Lua 5.4

luapure is a faithful Go port of PUC-Lua 5.4.8: the language, instruction set,
standard libraries, error messages, and edge cases are matched against the
reference C sources. This document is the single source of truth for **where it
differs from PUC and why** — and for the Go-native facilities it adds on top.

It has four parts:

1. [Structural divergences](#1-structural-divergences-same-observable-behavior) — implementation choices that keep the same observable behavior.
2. [Library-surface differences](#2-library-surface-differences) — standard-library functions that differ or are absent.
3. [Behavioral won't-fix](#3-behavioral-wont-fix) — semantics that cannot match because of the Go-native design.
4. [Go-native additions](#4-go-native-additions-not-in-puc) — embedding API that PUC has no equivalent for.

---

## 1. Structural divergences (same observable behavior)

These are deliberate implementation choices. Observable semantics, error
messages, and edge cases are still matched against the PUC sources.

- **GC** is delegated to the Go runtime (no `lmem` / incremental collector). Weak
  tables use `weak.Pointer` (Go 1.24); `__gc` uses `runtime.SetFinalizer` plus a
  main-thread drain queue. `collectgarbage` drives `runtime.GC()`.
- **`Value`** is a tagged struct (tag + inline scalar + GC pointer), not
  NaN-boxing — Go's precise GC must be able to follow pointers.
- **Errors** unwind via `panic`/`recover` instead of `longjmp`.
- **Coroutines** are goroutines handing off over channels, so a yield can cross
  a Go/"C" frame (e.g. inside a metamethod). Pure-Lua coroutines run stacklessly
  and never spawn a goroutine.
- **Binary chunks** use PUC-Lua 5.4's exact precompiled-chunk format (a port of
  `ldump.c`/`lundump.c`): a dump is byte-identical to `luac 5.4.8` on a 64-bit
  little-endian host, and luapure loads `luac` output and vice versa. (Internally
  luapure keeps absolute line numbers; dump recompresses them to PUC's
  `lineinfo`/`abslineinfo` and undump restores them.)
- **Memory-limit caps** (configurable `MaxTableArraySize`, `MaxLexElement`,
  constant count): Go cannot turn an allocation failure into a catchable error
  (OOM is a fatal runtime throw), so these size checks stand in for PUC's
  malloc-failure path. Default off; the conformance runner sets them.

## 2. Library-surface differences

The standard-library surface otherwise equals a **stock PUC 5.4.8 build**
(including the `LUA_COMPAT_5_3` defaults). The exceptions:

- **`io.popen` — absent.** Spawning a process and piping to it depends on the
  host process/OS, outside a portable pure-Go VM's scope. (`os.execute` is
  present.)
- **Dynamic C library loading — absent.** `require` cannot load `.so`/`.dll`
  modules, and `package.loadlib` always returns the `"absent"` failure — which
  is itself a valid PUC build (one compiled without `LUA_DL`). Register
  Go-native modules instead with [`Requiref`/`Preload`](#4-go-native-additions-not-in-puc).
- **Already at parity (formerly missing):** the deprecated `LUA_COMPAT_MATHLIB`
  functions `math.pow`, `math.sinh`, `math.cosh`, `math.tanh`, `math.log10`,
  `math.frexp`, `math.ldexp`, `math.atan2`, and the deprecated no-op
  `debug.setcstacklimit`, are present (pinned by `_glue5.4-tests/deprecated-compat.lua`).
  `io.tmpfile`, `file:setvbuf`, and `os.tmpname` are present.

## 3. Behavioral won't-fix

Consequences of being a Go-native, embeddable VM — not gaps to close:

- **`collectgarbage("count"/"step")` accounting.** The functions and modes
  exist, but exact byte accounting and the incremental step state cannot match:
  the Go runtime owns the heap. `collectgarbage("collect")` drives a real
  `runtime.GC()`; `"stop"/"restart"` are tracked for `isrunning` but do not
  actually pause the Go collector.
- **Conformance ceiling = 30/33.** Every official 5.4 test file that *can* pass,
  passes. The three that cannot:
  - `gc.lua` — the `collectgarbage` accounting above.
  - `files.lua` (process-dependent tail) — `os.execute`/`io.popen` and
    seekable-stdin assumptions depend on the host process/OS.
  - `all.lua` / `main.lua` — driver scripts, not behavioural fixtures.

## 4. Go-native additions (not in PUC)

PUC has no equivalent for these; they are the Go embedding surface. See the API
reference (`make doc` / `make doc-web`) for signatures.

- **State construction options** — `NewState(opts...)`: `WithOpenLibs`,
  `WithSandbox`, `WithContext`, and per-State limit overrides `WithMaxStack` /
  `WithMaxCCalls` / `WithMaxTableArraySize` (validated at construction). PUC
  bakes these limits in at compile time; luapure lets a pool give different
  States different limits in one process.
- **Cancellation** — `SetContext(ctx)` / `Context()`: cooperative cancellation
  checked between instructions, so a deadline or abort stops even a tight loop.
  A Go callback reads `Context()` to make its own blocking work cancellable.
- **Instruction budget** — `SetInstructionLimit(n)` / `InstructionCount()`: a
  Go-only cap on executed instructions (runaway-CPU guard), orthogonal to the
  wall-clock context and not reachable from `debug.sethook`.
- **Protected mode** — `WithRecoverGoPanics()` / `SetRecoverGoPanics(bool)`: opt
  in to recovering a non-LuaError Go panic from a callback into a catchable
  `*GoPanicError` (with the VM unwound so the State stays reusable). Off by
  default, which re-raises — PUC-faithful.
- **Sandboxing** — `NewSandbox()` (safe libraries only) and
  `RunWith(env, src, name)` / `CallProtoEnv(p, env, n)`: run a chunk under a
  custom `_ENV`, so a fresh env per call confines globals (the 5.4 `_ENV`
  sandbox) without recompiling.
- **Host modules** — `Requiref(name, open, glb)` and `Preload(name, open)`:
  register a Go-built module so `require(name)` resolves it (the embedding-side
  analog of `luaL_requiref` / `package.preload`).
- **Go ⇄ Lua data** — `ToValue` / `FromValue` conversion, full userdata with
  metatables and uservalues (`NewUserData`/`NewMetatable`/`CheckUserData`),
  structured errors (`*LuaError` with `Value()`/`Traceback()`). A callback raises
  errors with `ArgError`/`TypeError`, `RaiseError(format, …)` (PUC `luaL_error`,
  position-prefixed) or `RaiseValue(v)` (PUC `lua_error`, an arbitrary value).
- **Debugging** — a Go-native debug hook (`SetGoHook`) and a `Debugger`/`Session`
  with breakpoints/stepping, exposed over the Model Context Protocol (`debugmcp`)
  and the Debug Adapter Protocol (`debugdap`).

---

See also: [`README.md`](../README.md) · [`ROADMAP.md`](../ROADMAP.md) ·
[`FILEMAP.md`](FILEMAP.md). Lua is © 1994–2025 Lua.org, PUC-Rio (MIT); luapure
is a derivative work (MIT) — see [`LICENSE`](../LICENSE) / [`LICENSE-Lua`](../LICENSE-Lua).
