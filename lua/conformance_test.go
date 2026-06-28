package luapure

import "testing"

// Curated conformance gate for the from-scratch 5.4 VM: self-contained snippets
// exercising the semantics that distinguish Lua 5.4, drawn from the behaviours
// the official suite checks. Each runs end-to-end (parse -> codegen -> VM) with
// the standard libraries open and asserts a boolean result.

func assertLua(t *testing.T, name, src string) {
	t.Helper()
	L := NewState()
	L.OpenLibs()
	res, err := L.DoString("return ("+src+")", "="+name)
	if err != nil {
		t.Errorf("%s: error: %v", name, err)
		return
	}
	if len(res) == 0 || res[0].IsFalsy() {
		t.Errorf("%s: assertion failed: %s", name, src)
	}
}

func TestConformanceIntegerFloatSubtype(t *testing.T) {
	cases := map[string]string{
		"div_is_float":      `3 / 2 == 1.5 and math.type(6 / 2) == "float"`,
		"idiv_int":          `7 // 2 == 3 and math.type(7 // 2) == "integer"`,
		"idiv_float":        `7.0 // 2 == 3.0 and math.type(7.0 // 2) == "float"`,
		"pow_is_float":      `2 ^ 2 == 4.0 and math.type(2 ^ 2) == "float"`,
		"int_float_eq":      `1 == 1.0 and 0 == -0.0`,
		"modf_integer":      `select(2, math.modf(3)) == 0.0 and math.type((math.modf(3))) == "integer"`,
		"mixed_arith_int":   `math.type(2 + 3) == "integer" and math.type(2 + 3.0) == "float"`,
		"floor_returns_int": `math.type(math.floor(3.5)) == "integer"`,
		"tostring_float":    `tostring(1.0) == "1.0" and tostring(10) == "10"`,
		"int_overflow_wrap": `math.maxinteger + 1 == math.mininteger`,
	}
	for name, src := range cases {
		assertLua(t, name, src)
	}
}

func TestConformanceArithModulo(t *testing.T) {
	cases := map[string]string{
		"floored_mod_neg":   `(-5) % 3 == 1 and 5 % (-3) == -1`,
		"float_mod":         `5.5 % 2 == 1.5`,
		"idiv_floors":       `(-7) // 2 == -4`,
		"unary_minus_int":   `math.type(-(3)) == "integer"`,
		"bitwise_and_int":   `0xF0 & 0x0F == 0 and 0xFF & 0x0F == 0x0F`,
		"shift":             `1 << 62 == 4611686018427387904 and (-1) >> 63 == 1`,
		"bnot":              `~0 == -1 and ~5 == -6`,
	}
	for name, src := range cases {
		assertLua(t, name, src)
	}
}

func TestConformanceStringPatterns(t *testing.T) {
	cases := map[string]string{
		"find_plain":     `select(1, string.find("hello", "ll")) == 3`,
		"match_capture":  `string.match("2024-01-15", "(%d+)-(%d+)-(%d+)") == "2024"`,
		"gsub_count":     `select(2, string.gsub("aaa", "a", "b")) == 3`,
		"anchor":         `string.match("hello", "^h") == "h" and string.match("xhello", "^h") == nil`,
		"frontier":       `string.gsub("THE (quick) fox", "%f[%a]%u+%f[%A]", "X") == "X (quick) fox"`,
		"class_negation": `string.match("  abc", "%S+") == "abc"`,
		"balanced":       `string.match("(a(b)c)", "%b()") == "(a(b)c)"`,
		"star_greedy":    `string.match("<<x>>", "<(.-)>") == "<x"`,
		"format_g":       `string.format("%.2f", 3.14159) == "3.14"`,
	}
	for name, src := range cases {
		assertLua(t, name, src)
	}
}

func TestConformanceControlFlow(t *testing.T) {
	cases := map[string]string{
		"goto_forward": `(function() local x = 0; goto skip; x = 1; ::skip:: return x end)() == 0`,
		"numeric_for_count": `(function() local n = 0; for i = 1, 10 do n = n + 1 end; return n end)() == 10`,
		"for_no_run":   `(function() local n = 0; for i = 5, 1 do n = n + 1 end; return n end)() == 0`,
		"nested_break": `(function() local n=0; for i=1,3 do for j=1,3 do if j==2 then break end; n=n+1 end end; return n end)() == 3`,
		"and_or":       `(1 and 2) == 2 and (nil or 3) == 3 and (false and 1) == false`,
	}
	for name, src := range cases {
		assertLua(t, name, src)
	}
}

func TestConformanceClosuresAndScope(t *testing.T) {
	cases := map[string]string{
		"upvalue_shared": `(function() local x=0; local f=function() x=x+1 end; f(); f(); return x end)() == 2`,
		"per_iteration_local": `(function()
			local fns = {}
			for i = 1, 3 do fns[i] = function() return i end end
			return fns[1]() + fns[2]() + fns[3]()
		end)() == 6`,
		"recursive_local": `(function() local function f(n) if n<=1 then return 1 end return n*f(n-1) end return f(5) end)() == 120`,
	}
	for name, src := range cases {
		assertLua(t, name, src)
	}
}

func TestConformanceLibraryIdentities(t *testing.T) {
	L := NewState()
	L.OpenLibs()
	cases := map[string]string{
		"ipairs_identity":      `ipairs({}) == ipairs({})`,
		"next_identity":        `(function() local n1 = next; return n1 == next end)()`,
		"protected_metatable":  `(function()
			local a = setmetatable({}, {__metatable = "locked"})
			return getmetatable(a) == "locked" and pcall(setmetatable, a, {}) == false
		end)()`,
	}
	for name, src := range cases {
		res, err := L.DoString("return ("+src+")", "="+name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(res) == 0 || res[0].IsFalsy() {
			t.Errorf("%s: assertion failed", name)
		}
	}
}

func TestConformanceTablesAndMeta(t *testing.T) {
	cases := map[string]string{
		"length_border":    `#({1,2,3,nil}) >= 0 and #({1,2,3}) == 3`,
		"float_key_int":     `(function() local t={}; t[2.0]=5; return t[2] end)() == 5`,
		"index_chain":       `(function()
			local base = {x = 1}
			local mid = setmetatable({}, {__index = base})
			local t = setmetatable({}, {__index = mid})
			return t.x
		end)() == 1`,
		"newindex_intercept": `(function()
			local log = {}
			local t = setmetatable({}, {__newindex = function(_, k, v) rawset(log, k, v) end})
			t.a = 10
			return log.a
		end)() == 10`,
		"eq_metamethod": `(function()
			local mt = {__eq = function() return true end}
			local a, b = setmetatable({}, mt), setmetatable({}, mt)
			return a == b
		end)() == true`,
	}
	for name, src := range cases {
		assertLua(t, name, src)
	}
}
