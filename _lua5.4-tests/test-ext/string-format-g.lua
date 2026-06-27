-- lua-pure PUC-conformance regression: string.format %g/%G default precision
--
-- C's '%g'/'%G' default to 6 significant digits when no precision is given;
-- Go's fmt defaults to the shortest unique representation, so lua-pure (which
-- delegates to Go) printed e.g. "%g" of 0.1+0.2 as 0.30000000000000004
-- instead of 0.3. '%e'/'%f' already share C's default of 6 and are unaffected.
-- Pinned to PUC lua5.4.8.

local function eq(got, want, desc)
  assert(got == want,
    string.format("fmt-g [%s]: got %q, want %q", desc, got, want))
end

-- the headline cases: default %g rounds to 6 significant digits
eq(string.format("%g", 0.1 + 0.2), "0.3", "%g of 0.1+0.2")
eq(string.format("%g", math.pi), "3.14159", "%g of pi")
eq(string.format("%G", math.pi), "3.14159", "%G of pi")
eq(string.format("%g", 123456789), "1.23457e+08", "%g switches to exponent")
eq(string.format("%g", 0.0001234567), "0.000123457", "%g small fraction")

-- explicit precision is still honoured exactly
eq(string.format("%.3g", math.pi), "3.14", "%.3g")
eq(string.format("%.0g", math.pi), "3", "%.0g")
eq(string.format("%.14g", 0.1 + 0.2), "0.3", "%.14g still trims")
eq(string.format("%.17g", 0.1), "0.10000000000000001", "%.17g full precision")

-- flags / width / special values
eq(string.format("%#g", 1.0), "1.00000", "%#g keeps trailing zeros")
eq(string.format("%10g", math.pi), "   3.14159", "%g with width")
eq(string.format("%g", 100), "100", "%g of integer-valued")
eq(string.format("%g", 0.0), "0", "%g of zero")
eq(string.format("%g", -0.0), "-0", "%g of negative zero")
eq(string.format("%g", 1e20), "1e+20", "%g large exponent")
eq(string.format("%g", 1 / 0), "inf", "%g of inf")

-- %e / %f keep C's default of 6 (guard against a regression there)
eq(string.format("%e", 12345.678), "1.234568e+04", "%e default precision 6")
eq(string.format("%f", math.pi), "3.141593", "%f default precision 6")

print("string-format-g: all cases passed")
