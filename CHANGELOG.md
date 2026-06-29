# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

luapure ports PUC-Lua; the **Lua language version** it targets is called out
separately from the luapure release version.

## [Unreleased]

### Planned
- Track **PUC-Lua 5.5** as it stabilizes upstream (see [`ROADMAP.md`](ROADMAP.md)).
- Performance: keep narrowing the speed/allocation gap to the reference (see
  [`ROADMAP.md`](ROADMAP.md) for the current levers).

## [0.1.1] — 2026-06-29

Performance and documentation. No API or observable-behaviour change;
conformance stays at 30/33.

### Changed
- **Performance — arithmetic hot path.** Inlined the monomorphic arith/bit fast
  paths (`OP_ADD`/`SUB`/`MUL`, the bitwise ops, and `OP_ADDI`): replacing the
  shared function-pointer helpers with per-op variants lets the int/float ops
  inline as direct calls. A tight float loop drops from ~2.45× to ~2.2× PUC; the
  in-repo `ArithHeavyExec` benchmark is ~10% faster (`benchstat`, no allocation
  change). Behaviour is bit-identical.
- **Performance — pattern-match allocation.** `string.find`/`match`/`gsub` now
  recycle their `matchState` through a `sync.Pool` instead of allocating one per
  call; pattern-heavy workloads drop ~17% byte churn and ~6% of allocations.
  (`string.gmatch` keeps a per-call state — it escapes into the iterator.)

### Documentation
- Re-based the cross-engine benchmark table on steady-state runs: the
  `tree_build`/`string_build` workloads are sized to ~100 ms+ so fixed costs no
  longer inflate the ratios. Refreshed `docs/BENCHMARKS.md`, `README.md`, and
  `ROADMAP.md` (now ~1.2–1.3× on table/string, ~2.2× on a tight float loop).
- Added a Contributor Covenant code of conduct, GitHub issue forms, and a pull
  request template.

## [0.1.0] — 2026-06-28

First release. **Targets PUC-Lua 5.4.8.**

### Added
- **Engine.** Pure-Go port of PUC-Lua 5.4.8: instruction set, single-pass
  lexer+parser+codegen (no AST), and the VM dispatch loop.
- **Standard libraries.** base, string (incl. patterns and `string.pack`/
  `unpack`), table, math (xoshiro256\*\* PRNG, bit-identical to PUC), os, io,
  coroutine, utf8, debug, and `require`/`package`.
- **Precompiled chunks.** Dump/undump ported from `ldump.c`/`lundump.c`; output
  is byte-identical to `luac 5.4.8` on a 64-bit little-endian host, and luapure
  loads `luac` output and vice versa.
- **Embedding API.** Table create/read/write, Go-callback argument checking
  (`Check*`/`Opt*`), `ToValue`/`FromValue` conversion, structured `*LuaError`
  with `Value()`/`Traceback()`, userdata with metatables and uservalues,
  sandboxing (`NewSandbox`/`RunWith`), cooperative cancellation (`SetContext`),
  file loading (`LoadFile`/`DoFile`/`CompileReader`), and lifecycle `Close`.
- **Version identifiers.** `luapure.Version` / `VersionString()` and the Lua
  global `_LUAPURE_VERSION` ("luapure 0.1.0 (Lua 5.4.8)"); `_VERSION` stays
  exactly "Lua 5.4" for PUC conformance.
- **Debugger.** Breakpoint / step / pause core, exposed over the Model Context
  Protocol (stdio + HTTP, `debugmcp`) and the Debug Adapter Protocol (TCP,
  `debugdap`), with standalone binaries under `cmd/`.
- **Conformance.** 30/33 of the official Lua 5.4 test files pass — the PASS-able
  ceiling; the three remaining are structural won't-fixes (see `ROADMAP.md`).
- **Docs.** README with quickstart, `docs/FILEMAP.md` (PUC↔Go map), runnable
  embedding examples, `make doc`/`make doc-web`.

### Notes — intentional divergences from PUC
Observable semantics still match PUC; these are structural:
- GC delegated to the Go runtime; weak tables via `weak.Pointer`, `__gc` via
  `runtime.SetFinalizer` + a drain queue.
- `Value` is a tagged struct, not NaN-boxing (Go's precise GC must follow
  pointers).
- Errors unwind via `panic`/`recover` instead of `longjmp`.
- Coroutines are goroutines handing off over channels.
- Configurable memory-limit caps stand in for PUC's malloc-failure path (Go
  cannot turn OOM into a catchable error).

[Unreleased]: https://github.com/htcom-code/lua-pure/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/htcom-code/lua-pure/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/htcom-code/lua-pure/releases/tag/v0.1.0
