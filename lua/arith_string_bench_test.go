package luapure

import "testing"

// arithHeavySource is a number-crunching workload with no tables or strings on
// the hot path: an integer mixing loop (LCG + xorshift + idiv/mod/bitops) and a
// float polynomial-series loop. It surfaces the arithmetic opcodes, numeric
// for-loop control, and int/float coercion — the hotspots a table-heavy
// workload (tree_build) hides. Self-contained and re-run safe (locals only).
const arithHeavySource = `
local N = 200000
local acc = 0
for i = 1, N do
  acc = (acc * 1103515245 + 12345) & 0x7fffffff
  acc = acc ~ (acc >> 7)
  acc = acc + (i // 3) - (i % 5)
end
local fsum = 0.0
for i = 1, N do
  local x = i * 0.001
  fsum = fsum + (x*x*x - 2.5*x*x + 1.5*x - 0.75) / (x*x + 1.0)
end
return acc, fsum
`

// stringHeavySource is a text-processing workload: per iteration it builds a
// string (rep), rewrites it (gsub), upper-cases it, searches (find), measures
// (#, byte) and formats (format, sub). It surfaces MkString, the string lib,
// concat, and pattern matching — hotspots tree_build never touches.
// Self-contained and re-run safe (locals only).
const stringHeavySource = `
local N = 3000
local base = "The quick brown fox jumps over the lazy dog. "
local total = 0
for i = 1, N do
  local s = string.rep(base, 3)
  s = (string.gsub(s, "o", "0"))
  s = string.upper(s)
  local a = string.find(s, "FOX", 1, true)
  if a then total = total + a end
  total = total + #s
  total = total + string.byte(s, 1)
  local fmt = string.format("[%d:%05x:%s]", i, (i * 2654435761) & 0xffffff, string.sub(s, 1, 8))
  total = total + #fmt
end
return total
`

// BenchmarkArithHeavyExec re-runs the arith workload in one state, isolating
// arithmetic execution cost. Run with -benchmem / -cpuprofile.
func BenchmarkArithHeavyExec(b *testing.B) {
	p, err := CompileString(arithHeavySource, "@arith_heavy")
	if err != nil {
		b.Fatal(err)
	}
	L := NewState()
	L.OpenLibs()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		fn := L.loadProtoEnv(p, mkTable(L.globals))
		res := L.CallValue(fn, nil, 2)
		if len(res) != 2 {
			b.Fatalf("unexpected result %#v", res)
		}
	}
}

// BenchmarkStringHeavyExec re-runs the string workload in one state, isolating
// string-lib / MkString / pattern execution cost. Run with -benchmem / -cpuprofile.
func BenchmarkStringHeavyExec(b *testing.B) {
	p, err := CompileString(stringHeavySource, "@string_heavy")
	if err != nil {
		b.Fatal(err)
	}
	L := NewState()
	L.OpenLibs()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		fn := L.loadProtoEnv(p, mkTable(L.globals))
		res := L.CallValue(fn, nil, 1)
		if len(res) != 1 {
			b.Fatalf("unexpected result %#v", res)
		}
	}
}
