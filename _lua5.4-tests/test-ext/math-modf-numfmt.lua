-- lua-pure PUC-conformance regression: math.modf subtype + number extremes
--
-- The headline case: math.modf's integral part follows PUC's pushnumint --
-- an INTEGER when it fits in a lua_Integer, otherwise a float (so inf and
-- magnitudes past 2^63 stay float). lua-pure previously always returned a
-- float integral part. Also pins arithmetic-metamethod dispatch on both
-- operand sides and a few number-formatting extremes. Pinned to lua5.4.8.

local function eq(got, want, desc)
  assert(got == want,
    string.format("mnf [%s]: got %s, want %s", desc, tostring(got), tostring(want)))
end

-- math.modf: integral part is an integer when representable
do
  local i, f = math.modf(3.75)
  eq(i, 3, "modf int part value"); eq(math.type(i), "integer", "modf int part subtype")
  eq(f, 0.75, "modf frac part"); eq(math.type(f), "float", "modf frac subtype")
end
do
  local i, f = math.modf(-3.75)
  eq(i, -3, "modf neg int part"); eq(math.type(i), "integer", "modf neg int subtype")
  eq(f, -0.75, "modf neg frac")
end
do
  local i = math.modf(2.0 ^ 53) -- still fits int64
  eq(math.type(i), "integer", "modf 2^53 is integer")
end
do
  local i, f = math.modf(1 / 0) -- inf cannot be an integer
  eq(math.type(i), "float", "modf inf stays float")
  eq(tostring(i), "inf", "modf inf value"); eq(f, 0.0, "modf inf frac")
end
do
  local i = math.modf(2.0 ^ 63) -- overflows int64 range
  eq(math.type(i), "float", "modf 2^63 stays float")
end

-- math.floor/ceil share the same pushnumint rule
eq(math.type(math.floor(3.5)), "integer", "floor -> integer")
eq(math.type(math.ceil(3.5)), "integer", "ceil -> integer")
eq(math.type(math.floor(2.0 ^ 63)), "float", "floor past int64 stays float")

-- arithmetic metamethods dispatch from either operand
local mt = {__add = function() return "add" end, __sub = function() return "sub" end,
  __mul = function() return "mul" end, __band = function() return "band" end}
local m = setmetatable({}, mt)
eq(m + 1, "add", "__add lhs"); eq(1 + m, "add", "__add rhs")
eq(1 - m, "sub", "__sub rhs")
eq(m * 1, "mul", "__mul lhs")
eq(1 & m, "band", "__band rhs")

-- string operands still coerce in arithmetic (subtype follows the value)
eq("3" + "4", 7, "string coercion add"); eq(math.type("3" + "4"), "integer", "coerced add is integer")
eq("2.5" * "2", 5.0, "string coercion mul")

-- number-formatting extremes
eq(tostring(math.maxinteger), "9223372036854775807", "maxinteger tostring")
eq(tostring(math.mininteger), "-9223372036854775808", "mininteger tostring")
eq(math.maxinteger + 1 == math.mininteger, true, "integer overflow wraps")
eq(math.type(math.maxinteger + 0.0), "float", "int + float is float")
eq(string.format("%d", 3.0), "3", "%d on integral float")
eq(string.format("%a", 1.0), "0x1p+0", "%a of 1.0")
eq(string.format("%a", 0.25), "0x1p-2", "%a of 0.25")

print("math-modf-numfmt: all cases passed")
