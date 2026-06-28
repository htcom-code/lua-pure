-- lua-pure PUC-conformance: deprecated compat surface present in a STOCK PUC
-- 5.4.8 build. The math.* functions below come from LUA_COMPAT_MATHLIB (enabled
-- by default via LUA_COMPAT_5_3 in luaconf.h); debug.setcstacklimit is a
-- deprecated no-op the default build still exports. Pinned to lua5.4.8 — this
-- script passes on both PUC lua5.4 and luapure.

local function eq(got, want, desc)
  assert(got == want,
    string.format("compat [%s]: got %s, want %s", desc, tostring(got), tostring(want)))
end
local function approx(got, want, desc)
  assert(math.abs(got - want) < 1e-9,
    string.format("compat [%s]: got %s, want ~%s", desc, tostring(got), tostring(want)))
end

-- math.pow: x^y, always a float (PUC lua_pushnumber)
eq(math.pow(3, 2), 9.0, "pow value")
eq(math.type(math.pow(3, 2)), "float", "pow subtype")
approx(math.pow(2, 0.5), math.sqrt(2), "pow fractional exponent")

-- math.ldexp: x * 2^exp
eq(math.ldexp(1.0, 3), 8.0, "ldexp 1*2^3")
eq(math.ldexp(0.75, 2), 3.0, "ldexp 0.75*2^2")

-- math.frexp: x == frac * 2^exp, frac in [0.5, 1); exp is an integer
do
  local f, e = math.frexp(8.0)
  eq(f, 0.5, "frexp frac"); eq(e, 4, "frexp exp")
  eq(math.type(e), "integer", "frexp exp subtype")
end
do
  local f, e = math.frexp(0.0)
  eq(f, 0.0, "frexp zero frac"); eq(e, 0, "frexp zero exp")
end

-- hyperbolics (exact at 0, approx elsewhere)
eq(math.sinh(0), 0.0, "sinh 0")
eq(math.cosh(0), 1.0, "cosh 0")
eq(math.tanh(0), 0.0, "tanh 0")
approx(math.sinh(1), 1.1752011936438014, "sinh 1")

-- math.log10
eq(math.log10(1), 0.0, "log10 1")
approx(math.log10(1000), 3, "log10 1000")

-- math.atan2 is the alias for 5.4's two-argument math.atan(y, x); exact equality
-- because it is the very same computation.
eq(math.atan2(3, 4), math.atan(3, 4), "atan2 == atan(y,x)")
eq(math.atan2(0, 1), 0.0, "atan2 0,1")

-- debug.setcstacklimit: deprecated no-op; ignores its argument and returns
-- LUAI_MAXCCALLS (200 in the stock build).
eq(debug.setcstacklimit(1), 200, "setcstacklimit returns LUAI_MAXCCALLS")
eq(debug.setcstacklimit(99999), 200, "setcstacklimit ignores its argument")

print("deprecated-compat: all cases passed")
