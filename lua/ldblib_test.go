package luapure

import (
	"testing"
)

func TestDebugGetinfo(t *testing.T) {
	r := runLib(t, `
local function f(a, b) return a + b end
local info = debug.getinfo(f)
return info.what, info.nparams, info.isvararg, type(info.source), info.func == f`)
	wantStr(t, r[0], "Lua")
	wantInt(t, r[1], 2)
	wantBool(t, r[2], false)
	wantStr(t, r[3], "string")
	wantBool(t, r[4], true)
}

func TestDebugGetinfoLevel(t *testing.T) {
	r := runLib(t, `
local function inner() return debug.getinfo(1).currentline end
return type(inner())`)
	wantStr(t, r[0], "number")
}

func TestDebugUpvalues(t *testing.T) {
	r := runLib(t, `
local x = 10
local function f() return x end
local name, val = debug.getupvalue(f, 1)
debug.setupvalue(f, 1, 99)
return name, val, f()`)
	wantStr(t, r[0], "x")
	wantInt(t, r[1], 10)
	wantInt(t, r[2], 99)
}

func TestDebugUpvalueIdJoin(t *testing.T) {
	r := runLib(t, `
local x = 1
local z = 9
local function a() return x end
local function b() return z end
local sameA = debug.upvalueid(a, 1)
debug.upvaluejoin(b, 1, a, 1)
return debug.upvalueid(a, 1) == sameA, debug.upvalueid(b, 1) == sameA`)
	wantBool(t, r[0], true)
	wantBool(t, r[1], true)
}

func TestDebugGetlocal(t *testing.T) {
	r := runLib(t, `
local function f(a, b)
  local c = a + b
  local n1 = debug.getlocal(1, 1)
  local n3 = debug.getlocal(1, 3)
  return n1, n3
end
return f(1, 2)`)
	wantStr(t, r[0], "a")
	wantStr(t, r[1], "c")
}

func TestDebugSethookCount(t *testing.T) {
	L := NewState()
	L.OpenLibs()
	r, err := L.DoString(`
local n = 0
debug.sethook(function() n = n + 1 end, "", 1)
local s = 0
for i = 1, 50 do s = s + i end
debug.sethook()
return n > 0, s`, "=t")
	if err != nil {
		t.Fatal(err)
	}
	wantBool(t, r[0], true)
	wantInt(t, r[1], 1275)
}

func TestDebugSethookLine(t *testing.T) {
	r := runLib(t, `
local lines = {}
debug.sethook(function(ev, ln) lines[#lines+1] = ln end, "l")
local a = 1
local b = 2
local c = a + b
debug.sethook()
return #lines > 0`)
	wantBool(t, r[0], true)
}

func TestDebugMetatableRaw(t *testing.T) {
	r := runLib(t, `
local t = setmetatable({}, {__metatable = "locked"})
return debug.getmetatable(t).__metatable, getmetatable(t)`)
	wantStr(t, r[0], "locked")
	wantStr(t, r[1], "locked")
}

func TestStringFormatPointer(t *testing.T) {
	r := runLib(t, `
local t = {}
return string.format("%p", t):match("^0[xX]") ~= nil or string.format("%p", t):len() > 0,
       string.format("%p", 42)`)
	wantBool(t, r[0], true)
	wantStr(t, r[1], "(null)")
}
