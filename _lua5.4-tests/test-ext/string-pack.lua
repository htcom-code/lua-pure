-- lua-pure PUC-conformance regression: string.pack / unpack / packsize
--
-- The headline case: a missing data argument to string.pack reports
-- "got nil", not "got no value" -- PUC's str_pack pushes a nil to separate
-- the arguments from its buffer, so the first absent argument reads as that
-- nil. lua-pure reported "no value". Also pins sizes, endianness, alignment,
-- the string sub-formats, and round-trips. Pinned to PUC lua5.4.8.

local function eq(got, want, desc)
  assert(got == want,
    string.format("pack [%s]: got %s, want %s", desc, tostring(got), tostring(want)))
end
local function errtail(f, ...)
  local ok, msg = pcall(f, ...)
  assert(not ok, "expected error")
  return (tostring(msg):gsub("^[^:]*:%d+: ", ""))
end
local function hex(s) return (s:gsub(".", function(c) return string.format("%02x", c:byte()) end)) end

-- missing argument wording (the fixed case) vs an explicit nil: both "got nil"
eq(errtail(string.pack, "i2i2", 1), "bad argument #3 to 'string.pack' (number expected, got nil)", "missing arg -> nil")
eq(errtail(string.pack, "i2i2", 1, nil), "bad argument #3 to 'string.pack' (number expected, got nil)", "explicit nil")
-- a wrong type still names that type, and a normal pack is unaffected
eq(errtail(string.pack, "i2", "x"), "bad argument #2 to 'string.pack' (number expected, got string)", "wrong type")
eq(#string.pack("i2i2", 1, 2), 4, "normal pack still works")

-- sizes and signedness
eq(string.packsize("i1i2i4i8"), 15, "packsize i1..i8")
eq(string.packsize("j") == string.packsize("J"), true, "j and J same size")
eq(string.unpack("i2", string.pack("i2", -1000)), -1000, "signed round-trip")
eq(string.unpack("I2", string.pack("I2", 60000)), 60000, "unsigned round-trip")
eq(string.unpack("i3", string.pack("i3", 100000)), 100000, "odd-size int")
eq(errtail(string.pack, "i1", 200), "bad argument #2 to 'string.pack' (integer overflow)", "i1 overflow")

-- endianness
eq(hex(string.pack("<I4", 1)), "01000000", "little-endian")
eq(hex(string.pack(">I4", 1)), "00000001", "big-endian")
eq(hex(string.pack(">I2<I2", 1, 1)), "00010100", "endian switch mid-format")

-- alignment and padding
eq(string.packsize("!8 i1 i8"), 16, "align to 8")
eq(hex(string.pack("i1 x i1", 1, 2)), "010002", "explicit pad byte 'x'")
eq((function() local a, b = string.unpack("i2 i2", string.pack("i2 i2", 5, 6)) return a .. "," .. b end)(),
   "5,6", "spaces in format are ignored")

-- string sub-formats: cN (fixed), z (zero-terminated), sN (length-prefixed)
eq(string.unpack("c3", string.pack("c3", "abc")), "abc", "c3 fixed")
eq(errtail(string.pack, "c2", "abcd"), "bad argument #2 to 'string.pack' (string longer than given size)", "c2 too long")
eq(string.unpack("z", string.pack("z", "hello")), "hello", "z terminated")
eq(string.unpack("s2", string.pack("s2", "test")), "test", "s2 length-prefixed")
eq(errtail(string.pack, "z", "a\0b"), "bad argument #2 to 'string.pack' (string contains zeros)", "z rejects embedded zero")

-- floats and unpack position
eq(string.unpack("f", string.pack("f", 0.5)), 0.5, "float round-trip")
eq(string.unpack("d", string.pack("d", 1.25)), 1.25, "double round-trip")
eq((function() local _, pos = string.unpack("i4", string.pack("i4", 7) .. "X") return pos end)(), 5, "unpack returns next position")
eq(errtail(string.packsize, "s4"), "bad argument #1 to 'string.packsize' (variable-length format)", "packsize rejects variable size")

print("string-pack: all cases passed")
