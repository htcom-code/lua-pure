-- gopher-lua bugfix regression: string -> number coercion (arithmetic)
--
-- Pins the arithmetic/parseNumber coercion path to PUC Lua 5.1: decimal
-- strings are base 10 (leading zeros are NOT octal), only an explicit
-- 0x/0X prefix is hex, exponents and signs are honoured. Every `want`
-- below was captured from the reference interpreter lua-5.1.5 (`s + 0`).
--
-- Backs gopher-lua fork commit 99e7f7b (upstream #535, issue #478).
-- Reused as a PUC-conformance probe on the lua54-dev line.
--
-- NOTE: this exercises the *arithmetic* coercion (parseNumber). tonumber()
-- is a separate path and is tracked apart (1e3 / -0x10 still diverge there).

local function eq(got, want, desc)
  assert(got == want,
    string.format("numeric-coercion [%s]: got %s, want %s", desc, tostring(got), tostring(want)))
end

-- leading zeros are decimal, never octal (issue #478)
eq("00001" + 0, 1, "00001")
eq("00011" + 0, 11, "00011 (not octal 9)")
eq("010" + 0, 10, "010 (not octal 8)")
eq("007" + 0, 7, "007")
eq("00" + 0, 0, "00")
eq("-00011" + 0, -11, "-00011")
eq("+00007" + 0, 7, "+00007")

-- the original issue #478 reproduction (string + string)
eq("00001" + "00011", 12, "issue #478: 00001 + 00011")

-- explicit hex prefix (0x/0X), optionally signed
eq("0x7d6" + 0, 2006, "0x7d6")
eq("0xff" + 0, 255, "0xff")
eq("0XAB" + 0, 171, "0XAB (uppercase)")
eq("0x0" + 0, 0, "0x0")
eq("-0x10" + 0, -16, "-0x10 (signed hex)")
eq("-0X1f" + 0, -31, "-0X1f")
eq("0x7d6" + "1", 2007, "issue #478 hex: 0x7d6 + 1")

-- floats and exponents
eq("1.5" + 0, 1.5, "1.5")
eq("1e3" + 0, 1000, "1e3 (exponent)")
eq("1E2" + 0, 100, "1E2 (uppercase exponent)")

-- surrounding whitespace is trimmed
eq("  42  " + 0, 42, "whitespace")

print("numeric-coercion: all cases passed")
