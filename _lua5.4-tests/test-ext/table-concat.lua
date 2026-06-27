-- gopher-lua bugfix regression: table.concat
--
-- Old tableConcat pushed every element + separator onto the Lua stack and
-- folded them with stringConcat, overflowing the register stack on large
-- tables ("registry overflow"). It now builds the result with a Go
-- strings.Builder. Output is unchanged and matches PUC Lua 5.1.
-- Backs upstream yuin/gopher-lua#526. Reusable on lua54-dev.

local function eq(got, want, desc)
  assert(got == want,
    string.format("table-concat [%s]: got %q, want %q", desc, tostring(got), tostring(want)))
end

eq(table.concat({1, 2, 3}, ","), "1,2,3", "numbers with sep")
eq(table.concat({"a", "b", "c"}), "abc", "strings no sep")
eq(table.concat({10, 20, 30}, "-", 2, 3), "20-30", "range i..j")
eq(table.concat({"x", 1, "y"}, "|"), "x|1|y", "mixed string/number")
eq(table.concat({}, ","), "", "empty table")
eq(table.concat({42}, ","), "42", "single element")

-- large table must not overflow the register stack (issue: registry overflow)
local big = {}
for i = 1, 200000 do big[i] = i end
local s = table.concat(big, ",")
eq(#s, 1288894, "200k-element concat length")
assert(s:sub(1, 6) == "1,2,3,", "table-concat [big head]: " .. s:sub(1, 6))
assert(s:sub(-7) == ",200000", "table-concat [big tail]: " .. s:sub(-7))

print("table-concat: all cases passed")
