package luapure

import (
	"strings"
	"testing"
)

// runExpr compiles `return <body>` (or a full chunk) and returns the results.
func runChunk(t *testing.T, src string) []Value {
	t.Helper()
	L := NewState()
	res, err := L.DoString(src, "=test")
	if err != nil {
		t.Fatalf("DoString(%q) error: %v", src, err)
	}
	return res
}

func runChunkL(t *testing.T, L *LState, src string) []Value {
	t.Helper()
	res, err := L.DoString(src, "=test")
	if err != nil {
		t.Fatalf("DoString(%q) error: %v", src, err)
	}
	return res
}

func wantInt(t *testing.T, v Value, want int64) {
	t.Helper()
	if !v.IsInt() || v.AsInt() != want {
		t.Errorf("got %s, want int %d", describe(v), want)
	}
}

func wantFloat(t *testing.T, v Value, want float64) {
	t.Helper()
	if !v.IsFloat() || v.AsFloat() != want {
		t.Errorf("got %s, want float %g", describe(v), want)
	}
}

func wantStr(t *testing.T, v Value, want string) {
	t.Helper()
	if !v.IsString() || v.Str() != want {
		t.Errorf("got %s, want string %q", describe(v), want)
	}
}

func wantBool(t *testing.T, v Value, want bool) {
	t.Helper()
	if !v.IsBool() || v.AsBool() != want {
		t.Errorf("got %s, want bool %v", describe(v), want)
	}
}

func describe(v Value) string {
	switch {
	case v.IsInt():
		return "int(" + numToString(v) + ")"
	case v.IsFloat():
		return "float(" + numToString(v) + ")"
	case v.IsString():
		return "string(" + v.Str() + ")"
	case v.IsBool():
		if v.AsBool() {
			return "true"
		}
		return "false"
	case v.IsNil():
		return "nil"
	}
	return typeName(v)
}

func TestArithmetic(t *testing.T) {
	r := runChunk(t, "return 1+2, 10-3, 4*5, 2^10, 7//2, 7%3, 10/4")
	wantInt(t, r[0], 3)
	wantInt(t, r[1], 7)
	wantInt(t, r[2], 20)
	wantFloat(t, r[3], 1024) // ^ is always float
	wantInt(t, r[4], 3)
	wantInt(t, r[5], 1)
	wantFloat(t, r[6], 2.5) // / is always float
}

func TestIntFloatSubtype(t *testing.T) {
	r := runChunk(t, "return 3//2, 3.0//2, math and 1 or 6/2")
	wantInt(t, r[0], 1)
	wantFloat(t, r[1], 1.0)
	wantFloat(t, r[2], 3.0)
}

func TestBitwise(t *testing.T) {
	r := runChunk(t, "return 0xF0 & 0x0F, 0xF0 | 0x0F, 5 ~ 3, ~0, 1 << 4, 256 >> 2")
	wantInt(t, r[0], 0)
	wantInt(t, r[1], 0xFF)
	wantInt(t, r[2], 6)
	wantInt(t, r[3], -1)
	wantInt(t, r[4], 16)
	wantInt(t, r[5], 64)
}

func TestComparisonAndLogic(t *testing.T) {
	r := runChunk(t, "return 1 < 2, 2 <= 2, 3 > 4, 'a' < 'b', 1 == 1.0, nil == false")
	wantBool(t, r[0], true)
	wantBool(t, r[1], true)
	wantBool(t, r[2], false)
	wantBool(t, r[3], true)
	wantBool(t, r[4], true)
	wantBool(t, r[5], false)
}

func TestStringConcat(t *testing.T) {
	r := runChunk(t, `return "a" .. "b" .. "c", "x=" .. 42, 1 .. 2 .. 3`)
	wantStr(t, r[0], "abc")
	wantStr(t, r[1], "x=42")
	wantStr(t, r[2], "123")
}

func TestLengthOperator(t *testing.T) {
	r := runChunk(t, `return #"hello", #({1,2,3,4})`)
	wantInt(t, r[0], 5)
	wantInt(t, r[1], 4)
}

func TestIfElse(t *testing.T) {
	r := runChunk(t, `
local x = 10
if x > 5 then return "big" elseif x > 0 then return "small" else return "neg" end`)
	wantStr(t, r[0], "big")
}

func TestNumericForSum(t *testing.T) {
	r := runChunk(t, `
local s = 0
for i = 1, 100 do s = s + i end
return s`)
	wantInt(t, r[0], 5050)
}

func TestNumericForStepDown(t *testing.T) {
	r := runChunk(t, `
local t = {}
local n = 0
for i = 10, 1, -2 do n = n + 1; t[n] = i end
return n, t[1], t[5]`)
	wantInt(t, r[0], 5)
	wantInt(t, r[1], 10)
	wantInt(t, r[2], 2)
}

func TestFloatForLoop(t *testing.T) {
	r := runChunk(t, `
local c = 0
for x = 1.0, 2.0, 0.5 do c = c + 1 end
return c`)
	wantInt(t, r[0], 3)
}

func TestWhileAndBreak(t *testing.T) {
	r := runChunk(t, `
local i, s = 0, 0
while true do
  i = i + 1
  if i > 10 then break end
  s = s + i
end
return s`)
	wantInt(t, r[0], 55)
}

func TestRepeatUntil(t *testing.T) {
	r := runChunk(t, `
local i = 0
repeat i = i + 1 until i >= 5
return i`)
	wantInt(t, r[0], 5)
}

func TestFunctionsAndRecursion(t *testing.T) {
	r := runChunk(t, `
local function fact(n)
  if n <= 1 then return 1 end
  return n * fact(n-1)
end
return fact(5)`)
	wantInt(t, r[0], 120)
}

func TestClosuresAndUpvalues(t *testing.T) {
	r := runChunk(t, `
local function counter()
  local n = 0
  return function() n = n + 1; return n end
end
local c = counter()
return c(), c(), c()`)
	wantInt(t, r[0], 1)
	wantInt(t, r[1], 2)
	wantInt(t, r[2], 3)
}

func TestMultipleAssignAndReturn(t *testing.T) {
	r := runChunk(t, `
local function swap(a, b) return b, a end
local x, y = swap(1, 2)
return x, y`)
	wantInt(t, r[0], 2)
	wantInt(t, r[1], 1)
}

func TestVarargs(t *testing.T) {
	// Exercise vararg capture via a table constructor ({...}) — no library deps.
	r := runChunk(t, `
local function pack(...) return {...} end
local function count(...) local t = {...}; return #t end
local t = pack(10, 20, 30)
return t[1] + t[2] + t[3], #t, count(1, 2, 3, 4, 5)`)
	wantInt(t, r[0], 60)
	wantInt(t, r[1], 3)
	wantInt(t, r[2], 5)
}

func TestTableConstructAndIndex(t *testing.T) {
	r := runChunk(t, `
local t = {10, 20, 30, x = "hi", ["y"] = true}
return t[1], t[3], t.x, t.y, t[99]`)
	wantInt(t, r[0], 10)
	wantInt(t, r[1], 30)
	wantStr(t, r[2], "hi")
	wantBool(t, r[3], true)
	if !r[4].IsNil() {
		t.Errorf("t[99] should be nil, got %s", describe(r[4]))
	}
}

func TestTableMutation(t *testing.T) {
	r := runChunk(t, `
local t = {}
for i = 1, 5 do t[i] = i*i end
t[3] = nil
return #t >= 2, t[1], t[5], t[3]`)
	wantBool(t, r[0], true)
	wantInt(t, r[1], 1)
	wantInt(t, r[2], 25)
	if !r[3].IsNil() {
		t.Errorf("t[3] should be nil")
	}
}

func TestMetatableIndex(t *testing.T) {
	L := NewState()
	// setmetatable / getmetatable needed; provide minimal native versions.
	L.Register("setmetatable", func(L *LState) int {
		tbl := L.Arg(1)
		mt := L.Arg(2)
		if tbl.IsTable() && mt.IsTable() {
			tbl.tablev().meta = mt.tablev()
		}
		L.Push(tbl)
		return 1
	})
	r := runChunkL(t, L, `
local base = {greet = "hello"}
local t = setmetatable({}, {__index = base})
return t.greet`)
	wantStr(t, r[0], "hello")
}

func TestMetatableArithAndCall(t *testing.T) {
	L := NewState()
	L.Register("setmetatable", func(L *LState) int {
		tbl, mt := L.Arg(1), L.Arg(2)
		if tbl.IsTable() && mt.IsTable() {
			tbl.tablev().meta = mt.tablev()
		}
		L.Push(tbl)
		return 1
	})
	r := runChunkL(t, L, `
local V = {}
V.__add = function(a, b) return a.n + b.n end
V.__call = function(self, x) return self.n * x end
local a = setmetatable({n = 3}, V)
local b = setmetatable({n = 4}, V)
return a + b, a(10)`)
	wantInt(t, r[0], 7)
	wantInt(t, r[1], 30)
}

func TestGenericForWithNext(t *testing.T) {
	L := NewState()
	installPairsForTest(L)
	r := runChunkL(t, L, `
local t = {a=1, b=2, c=3}
local s = 0
for k, v in pairs(t) do s = s + v end
return s`)
	wantInt(t, r[0], 6)
}

func TestTailCallDeep(t *testing.T) {
	r := runChunk(t, `
local function loop(n, acc)
  if n == 0 then return acc end
  return loop(n - 1, acc + n)
end
return loop(100000, 0)`)
	wantInt(t, r[0], 5000050000)
}

func TestSelfMethodCall(t *testing.T) {
	r := runChunk(t, `
local obj = {value = 42}
function obj:get() return self.value end
return obj:get()`)
	wantInt(t, r[0], 42)
}

// installPairsForTest provides a minimal pairs() built on the VM's next().
func installPairsForTest(L *LState) {
	next := func(L *LState) int {
		tv := L.Arg(1)
		key := L.Arg(2)
		if !tv.IsTable() {
			return 0
		}
		nk, nv, ok, _ := tv.tablev().next(key)
		if !ok {
			L.Push(Nil)
			return 1
		}
		L.Push(nk)
		L.Push(nv)
		return 2
	}
	nextV := NewGoFunc("next", next)
	L.Register("pairs", func(L *LState) int {
		L.Push(nextV)
		L.Push(L.Arg(1))
		L.Push(Nil)
		return 3
	})
}

func TestErrorPropagation(t *testing.T) {
	L := NewState()
	_, err := L.DoString(`local x = nil; return x.field`, "=test")
	if err == nil {
		t.Fatal("expected runtime error indexing nil")
	}
	if !strings.Contains(err.Error(), "index") {
		t.Errorf("error %q should mention indexing", err.Error())
	}
}
