-- lua-pure PUC-conformance regression: variable info in runtime type errors
--
-- PUC appends a "(local 'x')" / "(global 'x')" / "(field 'x')" / "(upvalue
-- 'x')" descriptor to a type error when the offending value is a named
-- variable, but appends NOTHING when it is a bare constant (getobjname only
-- names a constant when it is a string). lua-pure previously emitted a
-- spurious "(constant '?')" for a numeric constant operand. Every `want`
-- was captured from PUC lua5.4.8.

local function errtail(f)
  local ok, msg = pcall(f)
  assert(not ok, "expected error")
  return (tostring(msg):gsub("^[^:]*:%d+: ", ""))
end
local function eq(got, want, desc)
  assert(got == want,
    string.format("varinfo [%s]: got %q, want %q", desc, got, want))
end

-- numeric constant operand: NO varinfo (the previously-broken "(constant '?')")
eq(errtail(function() return 1.5 & 2 end),
   "number has no integer representation", "const bitand lhs")
eq(errtail(function() return 2 & 1.5 end),
   "number has no integer representation", "const bitand rhs")

-- named local DOES get varinfo
eq(errtail(function() local x = 1.5 return x & 2 end),
   "number (local 'x') has no integer representation", "local bitand")

-- nil/field/global indexing keeps its varinfo
eq(errtail(function() local foo = nil return foo.bar end),
   "attempt to index a nil value (local 'foo')", "index nil local")
eq(errtail(function() return undefined_global_xyz.x end),
   "attempt to index a nil value (global 'undefined_global_xyz')", "index nil global")
eq(errtail(function() local t = {} return t.missing.x end),
   "attempt to index a nil value (field 'missing')", "index nil field")

-- a STRING constant, by contrast, IS named (getobjname names string constants)
eq(errtail(function() return ("abc")() end),
   "attempt to call a string value (constant 'abc')", "call string const")

-- a boolean constant is not named, but a boolean local is
eq(errtail(function() return (true).x end),
   "attempt to index a boolean value", "index bool const (no varinfo)")
eq(errtail(function() local b = true return b.x end),
   "attempt to index a boolean value (local 'b')", "index bool local")

print("error-varinfo: all cases passed")
