-- lua-pure PUC-conformance regression: number -> string formatting
--
-- Pins tostring()/print() to PUC Lua 5.4. Floats use LUAI_NUMFFORMAT
-- ("%.14g") with "inf"/"-inf"/"nan" spelling; integers print as exact
-- decimals (no exponent, no decimal point) regardless of magnitude. Every
-- `want` below was captured from the reference interpreter lua-5.4.8.
--
-- Adapted from the gopher-lua 5.1 bugfix probe (fork commit 8a482b0): the
-- sole 5.1->5.4 divergence here is 2^53, which is an integer literal in 5.4
-- and so prints as a plain decimal instead of "%.14g" exponential.

local function eq(got, want, desc)
  assert(got == want,
    string.format("number-format [%s]: got %q, want %q", desc, tostring(got), tostring(want)))
end

-- small integers (whole numbers print without a decimal point)
eq(tostring(0), "0", "zero")
eq(tostring(3), "3", "3")
eq(tostring(-5), "-5", "-5")
eq(tostring(42), "42", "42")
eq(tostring(100), "100", "100")
eq(tostring(1000000), "1000000", "1e6 as int")
eq(tostring(2147483648), "2147483648", "2^31")

-- integers print exact (any magnitude); floats hit the %.14g boundary at 1e14
eq(tostring(99999999999999), "99999999999999", "14 nines (integer)")
eq(tostring(9007199254740992), "9007199254740992", "2^53 (integer literal, plain decimal)")
eq(tostring(1e14), "1e+14", "1e14 (float boundary -> exponential)")
eq(tostring(1e15), "1e+15", "1e15")
eq(tostring(1e20), "1e+20", "1e20")
eq(tostring(1e100), "1e+100", "1e100")
eq(tostring(1e-10), "1e-10", "1e-10")
eq(tostring(0.000001), "1e-06", "1e-6")

-- fractions: %.14g keeps 14 significant digits (not Go's shortest form)
eq(tostring(0.1), "0.1", "0.1")
eq(tostring(0.2), "0.2", "0.2")
eq(tostring(0.3), "0.3", "0.3")
eq(tostring(1.5), "1.5", "1.5")
eq(tostring(-1.5), "-1.5", "-1.5")
eq(tostring(1/3), "0.33333333333333", "1/3")
eq(tostring(2/3), "0.66666666666667", "2/3")
eq(tostring(math.pi), "3.1415926535898", "math.pi")
eq(tostring(123456789.123456789), "123456789.12346", "mixed fixed form")

-- non-finite values: PUC spelling
eq(tostring(1/0), "inf", "+inf")
eq(tostring(-1/0), "-inf", "-inf")
eq(tostring(0/0), "nan", "nan")

print("number-format: all cases passed")
