# Benchmarks

Cross-engine speed of luapure against the reference C interpreter and the cgo
binding to it. **Read the ratios, not the absolute times** — ratios are measured
back-to-back and are robust; absolute milliseconds depend on the machine and its
load.

> For guarding *luapure's own* performance across changes, use the in-repo
> `go test -bench` benchmarks (`lua/*_bench_test.go`, e.g. `BenchmarkTreeBuildExec`)
> with `benchstat`. This document is the cross-engine snapshot.

## Engines

All three are **Lua 5.4** — an apples-to-apples comparison.

| label | engine | kind |
|---|---|---|
| **PUC** | reference C interpreter (`lua5.4`) | C |
| **golua** | [`aarzilli/golua`](https://github.com/aarzilli/golua) — cgo binding to the PUC-Lua 5.4 C library | Go **+ cgo → C** |
| **luapure** | this project | **pure Go** |

`golua` is the same C engine as PUC, reached through cgo, so it tracks PUC
closely. The comparison that matters for a Go embedder is therefore **pure-Go
luapure vs the cgo binding**: what you pay (or save) by *not* linking C.

## Results

Lower is faster. Ratio = engine ÷ PUC (1.00 = parity with the C interpreter).

| workload | PUC (s) | golua (cgo) | luapure (pure Go) |
|---|---:|---:|---:|
| `tree_build` — build + DFS-sum a 20k-node tree | 0.0096 | ~1.0× | **1.45×** |
| `arith` — 4M-iteration float loop | 0.122 | ~1.0× | **2.45×** |
| `string_build` — format + concat 120k strings | 0.064 | ~1.0× | **1.47×** |

**Takeaways**
- **golua (cgo) ≈ PUC** (~1.0×) — expected, since it executes the very same C
  interpreter; it mainly shows that the cgo call layer adds little for
  whole-script runs.
- **luapure (pure Go) costs ~1.3–1.6× on table/string work and ~2.45× on a tight
  float loop** versus the C engine — with **no cgo**: it cross-compiles, has no C
  toolchain dependency, and gives one independent VM per goroutine. Closing the
  arithmetic gap (alloc churn, hot-path) is the standing perf focus — see
  [`ROADMAP.md`](../ROADMAP.md).

## Provenance

| | |
|---|---|
| machine | Darwin x86_64 (Intel Core i7-4790K) |
| date | 2026-06-28 |
| luapure commit | `c87ce8f` |
| Go | 1.24.0 |
| golua | `aarzilli/golua` (cgo) against Homebrew `lua@5.4` (Lua 5.4.8), built `-tags "lua54 llua"` |
| method | same self-timed `.lua` on each engine; **best of 7** `os.clock()` CPU-time runs |

## How to reproduce

The workload scripts are in [`docs/bench/`](bench/); each self-times with
`os.clock()` and prints the best of 7 runs (seconds). Run the same file on each
engine:

```sh
# PUC reference
lua5.4 docs/bench/tree_build.lua

# luapure: a ~10-line runner (luapure has no standalone CLI)
cat > /tmp/luarun.go <<'EOF'
package main
import ("os"; luapure "github.com/htcom-code/lua-pure/lua")
func main() { L := luapure.NewState(); L.OpenLibs(); if _, err := L.DoFile(os.Args[1]); err != nil { panic(err) } }
EOF
# build it in a throwaway module that `replace`s luapure to your checkout, then:
/tmp/luarun docs/bench/tree_build.lua

# golua (aarzilli/golua, cgo → Lua 5.4 C). Needs the Lua 5.4 C lib (brew install lua).
# A runner must flush C stdio before exit:  L.DoFile(path); L.DoString("io.flush()")
PKG_CONFIG_PATH=$(brew --prefix lua@5.4)/lib/pkgconfig \
  CGO_LDFLAGS="$(pkg-config --libs-only-L lua5.4)" \
  go build -tags "lua54 llua" -o /tmp/golua54 .   # in a module requiring aarzilli/golua
DYLD_LIBRARY_PATH=$(brew --prefix lua@5.4)/lib /tmp/golua54 docs/bench/tree_build.lua
```

The dedicated five-engine harness (PUC, golua, gopher-lua stock & fork, luapure)
lives in a separate project; see brain `projects/lua-bench-harness`.

## Caveats — read before quoting a number

- **golua is cgo, not pure Go.** It links the C Lua 5.4 library, so it needs a C
  toolchain and the Lua headers/lib at build time, and a Lua dylib at run time —
  the opposite of luapure's "pure Go, cross-compiles anywhere" property. It is
  included as the *speed* reference an embedder would otherwise reach for, not as
  a like-for-like deployment option.
- **Absolute times are machine- and load-dependent** (this Intel host shows
  ±10–20% run to run). The **ratios** are measured back-to-back and hold; the
  millisecond figures do not transfer to other hardware. Re-measure on a quiet
  machine / Apple Silicon before quoting absolutes.
- **Three workloads only** — a snapshot, not a full corpus. They mirror luapure's
  own `lua/*_bench_test.go` shapes (table build, arithmetic, strings).
- Coroutine-heavy workloads are excluded: luapure's goroutine coroutines are much
  slower than the C engine's stackful ones on yield-bound loops (a known,
  separately-tracked area).
