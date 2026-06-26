package luapure

import "testing"

func TestPackUnpackInts(t *testing.T) {
	r := runLib(t, `
local s = string.pack("<i4", 0x01020304)
local a = string.unpack("<i4", s)
local b = string.unpack(">i4", string.pack(">i4", -100))
local big = string.pack("<i8", 0x0102030405060708)
return a, b, #s, #big, string.unpack("<i8", big)`)
	wantInt(t, r[0], 0x01020304)
	wantInt(t, r[1], -100)
	wantInt(t, r[2], 4)
	wantInt(t, r[3], 8)
	wantInt(t, r[4], 0x0102030405060708)
}

func TestPackEndianness(t *testing.T) {
	r := runLib(t, `
local le = string.pack("<I2", 0x1234)
local be = string.pack(">I2", 0x1234)
return string.byte(le, 1), string.byte(le, 2), string.byte(be, 1), string.byte(be, 2)`)
	wantInt(t, r[0], 0x34)
	wantInt(t, r[1], 0x12)
	wantInt(t, r[2], 0x12)
	wantInt(t, r[3], 0x34)
}

func TestPackFloatDouble(t *testing.T) {
	r := runLib(t, `
local f = string.pack("<f", 1.5)
local d = string.pack("<d", 3.14159)
return string.unpack("<f", f), string.unpack("<d", d), #f, #d`)
	wantFloat(t, r[0], 1.5)
	if !r[1].IsFloat() || r[1].AsFloat() < 3.1415 || r[1].AsFloat() > 3.1416 {
		t.Errorf("double roundtrip: %s", describe(r[1]))
	}
	wantInt(t, r[2], 4)
	wantInt(t, r[3], 8)
}

func TestPackString(t *testing.T) {
	r := runLib(t, `
local s = string.pack("s4", "hello")
local v, pos = string.unpack("s4", s)
local z = string.pack("z", "world")
local zv = string.unpack("z", z)
return v, zv, #z`)
	wantStr(t, r[0], "hello")
	wantStr(t, r[1], "world")
	wantInt(t, r[2], 6) // "world" + \0
}

func TestPacksize(t *testing.T) {
	r := runLib(t, `return string.packsize("i4"), string.packsize("<i8d"), string.packsize("BBB")`)
	wantInt(t, r[0], 4)
	wantInt(t, r[1], 16)
	wantInt(t, r[2], 3)
}

func TestPackMultiAndRoundtrip(t *testing.T) {
	r := runLib(t, `
local s = string.pack("<i4 i2 B", 1000, 200, 30)
local a, b, c, pos = string.unpack("<i4 i2 B", s)
return a, b, c, pos`)
	wantInt(t, r[0], 1000)
	wantInt(t, r[1], 200)
	wantInt(t, r[2], 30)
	wantInt(t, r[3], 8) // 4 + 2 + 1 = 7 bytes, next pos = 8
}

func TestUTF8Char(t *testing.T) {
	r := runLib(t, `
local s = utf8.char(72, 233, 108, 108, 111)
return #s, utf8.len(s)`)
	wantInt(t, r[0], 6) // é is 2 bytes
	wantInt(t, r[1], 5)
}

func TestUTF8Codepoint(t *testing.T) {
	r := runLib(t, `
local s = "aé中"
local a, b, c = utf8.codepoint(s, 1, #s)
return a, b, c`)
	wantInt(t, r[0], 97)    // 'a'
	wantInt(t, r[1], 233)   // 'é'
	wantInt(t, r[2], 20013) // '中'
}

func TestUTF8Codes(t *testing.T) {
	r := runLib(t, `
local s = "aé中"
local cps = {}
for p, c in utf8.codes(s) do cps[#cps+1] = c end
return #cps, cps[1], cps[2], cps[3]`)
	wantInt(t, r[0], 3)
	wantInt(t, r[1], 97)
	wantInt(t, r[2], 233)
	wantInt(t, r[3], 20013)
}

func TestUTF8Offset(t *testing.T) {
	r := runLib(t, `
local s = "aé中"
return utf8.offset(s, 1), utf8.offset(s, 2), utf8.offset(s, 3)`)
	wantInt(t, r[0], 1) // 'a' at byte 1
	wantInt(t, r[1], 2) // 'é' at byte 2
	wantInt(t, r[2], 4) // '中' at byte 4
}
