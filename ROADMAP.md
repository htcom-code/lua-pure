# Roadmap

luapure is a **pure-Go port of PUC-Lua**. Today it tracks the **5.4.8** reference
release: its instruction set, single-pass compiler, virtual machine, standard
libraries, and observable semantics are ported from `lua-5.4.8/src`, and a dump
is byte-identical to `luac 5.4.8` on a 64-bit little-endian host.

The project's standing commitment is to **follow PUC-Lua upstream** — to keep
porting new reference releases as they ship, the same way 5.4.8 was ported:
PUC-source-first, behaviour matched against the C sources, regressions pinned by
the conformance and ext-probe suites.

## Versioning intent

- **Current baseline — PUC-Lua 5.4.8.** Complete and the active line. Bug fixes
  and performance work land here.
- **Next — PUC-Lua 5.5.** As the 5.5 line stabilizes upstream, luapure will port
  it the same way: read the 5.5 sources first, port instruction/library/semantic
  changes, extend the conformance fixtures to the 5.5 test suite, and keep
  `luac`-byte-identity against the matching reference `luac`.

We track PUC releases rather than inventing language extensions; divergences are
limited to the Go-native adaptations documented in the README (GC, error
unwinding, coroutines, memory-limit caps).

## Done (5.4.8 line)

- PUC 5.4.8 instruction set, single-pass lexer+parser+codegen (no AST), and the
  VM dispatch loop.
- Standard libraries: base, string (incl. `pack`/`unpack`, patterns), table,
  math (xoshiro256\*\* PRNG, bit-identical), os, io, coroutine, utf8, debug,
  `require`/`package`.
- Precompiled-chunk dump/undump byte-identical to `luac 5.4.8`; loads `luac`
  output and vice versa.
- Embedding API: table exchange, Go-callback arg checking, `ToValue`/`FromValue`
  conversion, structured `*LuaError`, userdata + metatables + uservalues,
  sandboxing (`NewSandbox`/`RunWith`), context cancellation (`SetContext`), file
  loading, and lifecycle `Close`.
- Debugger: breakpoints / step / pause core, exposed over the Model Context
  Protocol (stdio + HTTP) and the Debug Adapter Protocol (TCP).
- Conformance: **30/33** of the official Lua 5.4 test files pass — the PASS-able
  ceiling (see Known limits).

## Known limits (won't-fix on the 5.4 line)

Structural consequences of being a Go-native, embeddable VM, not gaps to close:
`collectgarbage("count"/"step")` accounting (Go owns the heap), `io.popen` and
dynamic C library loading (host/OS-dependent). These cap conformance at **30/33**
— every official test file that *can* pass, passes (`gc.lua`, the
process-dependent tail of `files.lua`, and the `all.lua`/`main.lua` drivers are
the three that cannot).

Full detail in [`docs/COMPATIBILITY.md`](docs/COMPATIBILITY.md).

## Performance — an ongoing commitment on the 5.4 line

**Performance work on the 5.4 line continues indefinitely.** Functional parity
with PUC is reached, but luapure keeps narrowing the speed/allocation gap to the
reference. The discipline is fixed:

- **Measurement-first** — reproduce a benchmark, profile, optimize, then confirm
  with an interleaved `benchstat` comparison; no change ships on intuition.
- **Never at the cost of fidelity** — every perf change must keep the PUC
  behaviour match and the conformance suite (30/33) green; speed never trumps
  correctness.
- **No silent regressions** — the `lua/*_bench_test.go` benchmarks (e.g.
  `BenchmarkTreeBuildExec`, `BenchmarkProtectedCall`) are the guardrail.

Current standing vs other engines (latest snapshot in [`docs/BENCHMARKS.md`](docs/BENCHMARKS.md)):
faster than the go-lua 5.1 fork on every workload, and within ~1.5× of the
reference C interpreter on table/string work (~2.4× on a tight float loop).

Current levers, ordered by intent — not commitments:

- **Execution alloc churn** — reduce per-instruction allocation frequency to cut
  GC pressure (the standing #1 lever; tree-build exec still allocates more than a
  reference fork).
- **Table get/set hot path** — trim bounds-check / indirection cost in
  `rawget`/`rawgetInt` (profile-identified hot spot).
- **Runtime string interning** — on hold; compile-time interning already covers
  the measured win. Reopen only with a benchmark justification.

(Shipped: the per-State limit options and `SetInstructionLimit` — see
[`docs/COMPATIBILITY.md`](docs/COMPATIBILITY.md).)

## Contributing to the roadmap

The porting discipline (PUC-source-first, behaviour-matched, ext-probe pinned)
is in [`CONTRIBUTING.md`](CONTRIBUTING.md). Release history is in
[`CHANGELOG.md`](CHANGELOG.md).
