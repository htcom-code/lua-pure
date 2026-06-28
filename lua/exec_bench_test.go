package luapure

import "testing"

// treeBuildSource is the harness's exec workload (lua54
// scripts/advance/tree_build.lua): build a balanced binary tree from a flat map
// of N positional-record nodes, then iterative-DFS it. Pure in-memory table
// work, fully local, so re-running the chunk in the same state is independent.
// It is the reference workload for exec alloc-churn measurement.
const treeBuildSource = `
local N = 20000
local ID, PARENT, VALUE, CHILDREN = 1, 2, 3, 4
local nodes = {}
for i = 1, N do
  nodes[i] = {
    i,
    (i > 1) and math.floor(i / 2) or 0,
    (i * 2654435761) % 1000003,
    {},
  }
end
local roots, nroots = {}, 0
for i = 1, N do
  local n = nodes[i]
  if n[PARENT] == 0 then
    nroots = nroots + 1
    roots[nroots] = n
  else
    local pc = nodes[n[PARENT]][CHILDREN]
    pc[#pc + 1] = n
  end
end
local total, maxdepth, sum = 0, 0, 0
local snode, sdepth, top = {}, {}, 0
for r = 1, nroots do
  top = top + 1
  snode[top] = roots[r]
  sdepth[top] = 1
end
while top > 0 do
  local node, depth = snode[top], sdepth[top]
  snode[top] = nil
  top = top - 1
  total = total + 1
  sum = sum + node[VALUE]
  if depth > maxdepth then maxdepth = depth end
  local ch = node[CHILDREN]
  for j = 1, #ch do
    top = top + 1
    snode[top] = ch[j]
    sdepth[top] = depth + 1
  end
end
return total, maxdepth, sum
`

// coroutineYieldSource drives a pure-Lua generator through N resume/yield
// cycles: the body yields i each step and never crosses a Go-recursion boundary
// (no pcall / metamethod / generic-for between yield and resume), so it is the
// fast-path target for the stackless redesign. Per execution it pays one
// coroutine setup plus N yield round-trips, so for large N the per-yield handoff
// cost dominates — the figure the redesign aims to cut.
const coroutineYieldSource = `
local N = 10000
local co = coroutine.wrap(function()
  for i = 1, N do coroutine.yield(i) end
end)
local s = 0
for i = 1, N do s = s + co() end
return s
`

// BenchmarkCoroutineYield measures N pure-Lua resume/yield cycles per op. With
// the goroutine-per-coroutine model each cycle is two channel handoffs; this is
// the baseline the stackless fast path is measured against. Run with -benchmem.
func BenchmarkCoroutineYield(b *testing.B) {
	p, err := CompileString(coroutineYieldSource, "@coroutine_yield")
	if err != nil {
		b.Fatal(err)
	}
	L := NewState()
	L.OpenLibs()
	L.SetGlobal("print", NewGoFunc("print", func(*LState) int { return 0 }))
	const want = 10000 * (10000 + 1) / 2 // sum 1..N
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		fn := L.loadProtoEnv(p, mkTable(L.globals))
		res := L.CallValue(fn, nil, 1)
		if len(res) != 1 || res[0].AsInt() != want {
			b.Fatalf("unexpected result %#v", res)
		}
	}
}

// BenchmarkTreeBuildExec compiles the tree_build workload once, then re-runs the
// chunk each iteration in a single state — isolating execution alloc churn from
// compile and stdlib-init cost. Run with -benchmem (and -memprofile) to drive
// the alloc-churn work.
func BenchmarkTreeBuildExec(b *testing.B) {
	p, err := CompileString(treeBuildSource, "@tree_build")
	if err != nil {
		b.Fatal(err)
	}
	L := NewState()
	L.OpenLibs()
	// Silence print so the benchmark does no I/O (the workload returns values).
	L.SetGlobal("print", NewGoFunc("print", func(*LState) int { return 0 }))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		fn := L.loadProtoEnv(p, mkTable(L.globals))
		res := L.CallValue(fn, nil, 3)
		if len(res) != 3 || res[0].AsInt() != 20000 {
			b.Fatalf("unexpected result %#v", res)
		}
	}
}

// protectedCallSource stresses the protected-call path — pcall's defer/recover
// in ldo.go — with many pcalls per run. This is the workload that would expose
// any hot-path cost of the opt-in Go-panic recover mode (which only changes the
// recover branch, taken on panic), so it serves as the before/after baseline.
const protectedCallSource = `
local function f(x) return x + 1 end
local s = 0
for i = 1, 200000 do
  local ok, v = pcall(f, i)
  s = s + v
end
return s
`

func BenchmarkProtectedCall(b *testing.B) {
	p, err := CompileString(protectedCallSource, "@pcall")
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
