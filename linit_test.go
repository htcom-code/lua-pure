package luapure

import "testing"

func runLib(t *testing.T, src string) []Value {
	t.Helper()
	L := NewState()
	L.OpenLibs()
	res, err := L.DoString(src, "=test")
	if err != nil {
		t.Fatalf("DoString(%q) error: %v", src, err)
	}
	return res
}

func TestBaseTypeTostringTonumber(t *testing.T) {
	r := runLib(t, `return type(1), type("x"), type({}), type(print), tostring(nil), tostring(42), tonumber("3.5"), tonumber("ff", 16)`)
	wantStr(t, r[0], "number")
	wantStr(t, r[1], "string")
	wantStr(t, r[2], "table")
	wantStr(t, r[3], "function")
	wantStr(t, r[4], "nil")
	wantStr(t, r[5], "42")
	wantFloat(t, r[6], 3.5)
	wantInt(t, r[7], 255)
}

func TestBasePcall(t *testing.T) {
	r := runLib(t, `
local ok, err = pcall(function() error("boom") end)
local ok2, v = pcall(function() return 99 end)
return ok, ok2, v`)
	wantBool(t, r[0], false)
	wantBool(t, r[1], true)
	wantInt(t, r[2], 99)
}

func TestBaseAssertAndError(t *testing.T) {
	// PUC assert tail-calls error(): a string message gets the caller's
	// position prepended (level 1), so it reads "test:LINE: custom".
	r := runLib(t, `
local ok, msg = pcall(function() assert(false, "custom") end)
local hasPos = msg:match("^test:%d+: custom$") ~= nil
return ok, hasPos, (msg:gsub("^.-:%d+: ", ""))`)
	wantBool(t, r[0], false)
	wantBool(t, r[1], true)
	wantStr(t, r[2], "custom")
}

func TestIpairsPairsSelect(t *testing.T) {
	r := runLib(t, `
local t = {10, 20, 30}
local s = 0
for i, v in ipairs(t) do s = s + v end
local m = {a=1, b=2, c=3}
local total = 0
for k, v in pairs(m) do total = total + v end
return s, total, select('#', 1, 2, 3), select(2, 'a', 'b', 'c')`)
	wantInt(t, r[0], 60)
	wantInt(t, r[1], 6)
	wantInt(t, r[2], 3)
	wantStr(t, r[3], "b")
}

func TestStringBasics(t *testing.T) {
	r := runLib(t, `
return ("hello"):upper(), string.sub("hello", 2, 4), string.rep("ab", 3),
       string.reverse("abc"), string.len("héllo" ), string.byte("A"), string.char(66, 67)`)
	wantStr(t, r[0], "HELLO")
	wantStr(t, r[1], "ell")
	wantStr(t, r[2], "ababab")
	wantStr(t, r[3], "cba")
	wantInt(t, r[4], 6) // bytes, é is 2 bytes in utf8 source
	wantInt(t, r[5], 65)
	wantStr(t, r[6], "BC")
}

func TestStringFormat(t *testing.T) {
	r := runLib(t, `return string.format("%d-%s-%05.2f-%x", 42, "hi", 3.14159, 255)`)
	wantStr(t, r[0], "42-hi-03.14-ff")
}

func TestStringArithCoercion(t *testing.T) {
	r := runLib(t, `return "10" + 5, "3" * "4", -"7", "2" ^ "3"`)
	wantInt(t, r[0], 15)
	wantInt(t, r[1], 12)
	wantInt(t, r[2], -7)
	wantFloat(t, r[3], 8.0)
}

func TestStringFindMatch(t *testing.T) {
	r := runLib(t, `
local s, e = string.find("hello world", "world")
local m = string.match("key=value", "(%w+)=(%w+)")
local m1, m2 = string.match("key=value", "(%w+)=(%w+)")
return s, e, m1, m2`)
	wantInt(t, r[0], 7)
	wantInt(t, r[1], 11)
	wantStr(t, r[2], "key")
	wantStr(t, r[3], "value")
}

func TestStringGmatch(t *testing.T) {
	r := runLib(t, `
local words = {}
for w in string.gmatch("the quick brown fox", "%a+") do
  words[#words+1] = w
end
return #words, words[1], words[4]`)
	wantInt(t, r[0], 4)
	wantStr(t, r[1], "the")
	wantStr(t, r[2], "fox")
}

func TestStringGsub(t *testing.T) {
	r := runLib(t, `
local s, n = string.gsub("hello world", "o", "0")
local s2 = string.gsub("hello", "l", function(c) return c:upper() end)
local s3 = string.gsub("$name", "%$(%w+)", {name = "Lua"})
return s, n, s2, s3`)
	wantStr(t, r[0], "hell0 w0rld")
	wantInt(t, r[1], 2)
	wantStr(t, r[2], "heLLo")
	wantStr(t, r[3], "Lua")
}

func TestTableLib(t *testing.T) {
	r := runLib(t, `
local t = {3, 1, 2}
table.insert(t, 4)
table.insert(t, 1, 0)
local removed = table.remove(t, 1)
table.sort(t)
return table.concat(t, ","), removed, #t`)
	wantStr(t, r[0], "1,2,3,4")
	wantInt(t, r[1], 0)
	wantInt(t, r[2], 4)
}

func TestTablePackUnpack(t *testing.T) {
	r := runLib(t, `
local p = table.pack(10, 20, 30)
local a, b, c = table.unpack(p)
return p.n, a, b, c`)
	wantInt(t, r[0], 3)
	wantInt(t, r[1], 10)
	wantInt(t, r[2], 20)
	wantInt(t, r[3], 30)
}

func TestTableSortWithComparator(t *testing.T) {
	r := runLib(t, `
local t = {1, 2, 3, 4, 5}
table.sort(t, function(a, b) return a > b end)
return table.concat(t, ",")`)
	wantStr(t, r[0], "5,4,3,2,1")
}

func TestMathLib(t *testing.T) {
	r := runLib(t, `
return math.floor(3.7), math.ceil(3.2), math.abs(-5), math.max(1, 9, 3),
       math.min(4, 2, 8), math.sqrt(16), math.type(1), math.type(1.0),
       math.tointeger(5.0), math.maxinteger`)
	wantInt(t, r[0], 3)
	wantInt(t, r[1], 4)
	wantInt(t, r[2], 5)
	wantInt(t, r[3], 9)
	wantInt(t, r[4], 2)
	wantFloat(t, r[5], 4.0)
	wantStr(t, r[6], "integer")
	wantStr(t, r[7], "float")
	wantInt(t, r[8], 5)
	wantInt(t, r[9], 9223372036854775807)
}

func TestXpcall(t *testing.T) {
	r := runLib(t, `
local ok, msg = xpcall(function() error("oops") end, function(e) return "handled: " .. e end)
return ok, msg`)
	wantBool(t, r[0], false)
	if !r[1].IsString() || r[1].Str() == "" {
		t.Errorf("xpcall handler result missing: %s", describe(r[1]))
	}
}

func TestClosureCounterViaLib(t *testing.T) {
	r := runLib(t, `
local function make()
  local t = {}
  return function(k, v) if v then t[k] = v else return t[k] end end
end
local m = make()
m("a", 1)
m("b", 2)
return m("a") + m("b")`)
	wantInt(t, r[0], 3)
}
