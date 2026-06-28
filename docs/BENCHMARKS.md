# Benchmarks

Cross-engine speed of luapure against the reference C interpreter and a pure-Go
Lua 5.1 engine. **Read the ratios, not the absolute times** — ratios are
measured back-to-back and are robust; absolute milliseconds depend on the
machine and its load.

> For guarding *luapure's own* performance across changes, use the in-repo
> `go test -bench` benchmarks (`lua/*_bench_test.go`, e.g. `BenchmarkTreeBuildExec`)
> with `benchstat`. This document is the cross-engine snapshot.

## Engines

| label | engine | Lua version |
|---|---|---|
| **PUC** | reference C interpreter (`lua5.4`) | 5.4.8 |
| **go-lua** | gopher-lua fork (`git.htcom.co.kr/golang/gopher-lua`) | **5.1** |
| **luapure** | this project | 5.4.8 (port) |

## Results

Lower is faster. Ratio = engine ÷ PUC (1.00 = parity with C; smaller is better).

| workload | PUC (s) | go-lua | luapure |
|---|---:|---:|---:|
| `tree_build` — build + DFS-sum a 20k-node tree | 0.0099 | **2.39×** | **1.48×** |
| `arith` — 4M-iteration float loop | 0.1222 | **4.14×** | **2.44×** |
| `string_build` — format + concat 120k strings | 0.0653 | **3.48×** | **1.49×** |

**Takeaways**
- luapure is **faster than the go-lua (5.1) fork on every workload** — ~1.5–1.7×
  ahead.
- vs the reference C interpreter, luapure is within **~1.5×** on table- and
  string-heavy work and **~2.4×** on a tight float-arithmetic loop. Closing the
  arithmetic gap (alloc churn, hot-path) is the standing perf focus — see
  [`ROADMAP.md`](../ROADMAP.md).

## Provenance

| | |
|---|---|
| machine | Darwin x86_64 (Intel Core i7-4790K) |
| date | 2026-06-28 |
| luapure commit | `444e34c` |
| Go | 1.24.0 |
| method | same self-timed `.lua` on each engine; **best of 7** `os.clock()` CPU-time runs |

## How to reproduce

The workload scripts are in [`docs/bench/`](bench/); each self-times with
`os.clock()` and prints the best of 7 runs (seconds). Run the same file on each
engine:

```sh
# PUC reference
lua5.4 docs/bench/tree_build.lua

# go-lua (gopher-lua fork): build its glua runner once
go build -o /tmp/glua git.htcom.co.kr/golang/gopher-lua/cmd/glua   # from a fork checkout
/tmp/glua docs/bench/tree_build.lua

# luapure: a ~10-line runner (luapure has no standalone CLI)
cat > /tmp/luarun.go <<'EOF'
package main
import ("os"; luapure "github.com/htcom-code/lua-pure/lua")
func main() { L := luapure.NewState(); L.OpenLibs(); if _, err := L.DoFile(os.Args[1]); err != nil { panic(err) } }
EOF
# build it in a throwaway module that `replace`s luapure to your checkout, then:
/tmp/luarun docs/bench/tree_build.lua
```

## Caveats — read before quoting a number

- **go-lua is Lua 5.1**, so its numbers are all floats; PUC and luapure are 5.4
  with an integer subtype. The `arith` loop in particular exercises different
  number paths across versions — this is a cross-*version* comparison, not
  identical semantics. (5.1 is the comparison point because the fork is the
  pure-Go engine luapure would replace.)
- **Absolute times are machine- and load-dependent** (this Intel host shows
  ±10–20% run to run). The **ratios** are measured back-to-back and hold; the
  millisecond figures do not transfer to other hardware. Re-measure on a quiet
  machine / Apple Silicon before quoting absolutes.
- **Three workloads only** — a snapshot, not a full corpus. They mirror luapure's
  own `lua/*_bench_test.go` shapes (table build, arithmetic, strings).
- Coroutine-heavy workloads are intentionally excluded here: luapure's goroutine
  coroutines are far slower than PUC's stackful ones on yield-bound loops (a
  known, separately-tracked area), and the 5.1 fork's model differs again.
