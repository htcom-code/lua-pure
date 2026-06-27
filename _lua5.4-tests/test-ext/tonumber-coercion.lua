-- lua-pure PUC-conformance regression: tonumber() string -> number
--
-- Pins tonumber() to PUC Lua 5.4. With NO base argument the default-base
-- path accepts the full numeral grammar (decimal, hex incl. signed &
-- hex-float, exponent). With ANY explicit base argument — base 10 included —
-- 5.4 does a pure integer parse in that base, so floats/exponents/0x-prefix
-- are rejected (nil). Every `want` was captured from lua-5.4.8 (LC_ALL=C).
--
-- Adapted from the gopher-lua 5.1 bugfix probe: 5.1 routed an explicit
-- base 10 back through the full grammar, so ("1e3",10)/("3.14",10)/
-- ("0x10",10) parsed there; 5.4 treats the base argument as integer-only,
-- which is the divergence captured below.
--
-- NOTE: "inf"/"nan" parsing is intentionally omitted — PUC delegates those
-- to the C strtod, so acceptance is platform-dependent.

local function eq(got, want, desc)
  assert(got == want,
    string.format("tonumber [%s]: got %s, want %s", desc, tostring(got), tostring(want)))
end

-- exponent notation, default base (no base arg = full numeral grammar)
eq(tonumber("1e3"), 1000, "1e3")
eq(tonumber("2e2"), 200, "2e2")
eq(tonumber("1E2"), 100, "1E2 (uppercase)")
eq(tonumber("5e-07"), 0.0000005, "5e-07 (negative exponent)")
-- 5.4: an explicit base (even 10) is integer-only, so a float/exponent is nil
eq(tonumber("1e3", 10), nil, "1e3 base 10 -> nil (integer-only)")
eq(tonumber("3.14", 10), nil, "3.14 base 10 -> nil (integer-only)")

-- hex, including signed and strings containing 'e'/'f' (must NOT hit ParseFloat)
eq(tonumber("0xF"), 15, "0xF")
eq(tonumber("0xff"), 255, "0xff")
eq(tonumber("0xabe"), 2750, "0xabe (hex with e)")
eq(tonumber("0xfee"), 4078, "0xfee (hex with ee)")
eq(tonumber("0x10"), 16, "0x10")
eq(tonumber("0x10", 10), nil, "0x10 base 10 -> nil (0x invalid in integer base 10)")
eq(tonumber("-0x10"), -16, "-0x10 (signed hex)")
eq(tonumber("0x1p4"), 16, "0x1p4 (hex float)")

-- explicit non-10 base: integer parse in that base (no float/exponent)
eq(tonumber("2e2", 15), 662, "2e2 base 15")
eq(tonumber("1e", 16), 30, "1e base 16")
eq(tonumber("ff", 16), 255, "ff base 16")
eq(tonumber("10", 2), 2, "10 base 2")
eq(tonumber("ff", 10), nil, "ff base 10 -> nil")

-- plain decimal and floats
eq(tonumber("100"), 100, "100")
eq(tonumber("3.14"), 3.14, "3.14")
eq(tonumber("  42 "), 42, "whitespace trimmed")

-- junk -> nil
eq(tonumber(""), nil, "empty")
eq(tonumber("0x"), nil, "0x alone")

print("tonumber-coercion: all cases passed")
