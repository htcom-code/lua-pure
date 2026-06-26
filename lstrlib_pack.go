package luapure

import (
	"math"
	"strings"
	"unsafe"
)

// string.pack / unpack / packsize (lstrlib.c), a faithful port of the binary
// (de)serialisation format: endianness control, sized integers, floats,
// fixed/length-prefixed/zero-terminated strings, padding and alignment.

const (
	kInt = iota
	kUint
	kFloat
	kNumber
	kDouble
	kChar
	kString
	kZstr
	kPadding
	kPaddalign
	kNop
)

const (
	packSZINT      = 8          // sizeof(lua_Integer)
	packMAXINTSIZE = 16         // MAXINTSIZE
	packMAXSIZE    = 0x7fffffff // MAXSIZE (INT_MAX); size_t >= int on host
	packSZSIZET    = 8          // sizeof(size_t)
)

// nativeLittle reports the host byte order.
var nativeLittle = func() bool {
	var x uint16 = 1
	return *(*byte)(unsafe.Pointer(&x)) == 1
}()

type packHeader struct {
	little   bool
	maxalign int
}

func packDigit(c byte) bool { return c >= '0' && c <= '9' }

func packGetnum(fmt []byte, i *int, df int) int {
	if *i >= len(fmt) || !packDigit(fmt[*i]) {
		return df
	}
	// Matches PUC getnum: stop accumulating once another digit would risk
	// overflow, leaving the remaining digits in the format string (so they are
	// re-read as the next option and rejected). This is what makes
	// packsize("c1"..("0"):rep(40)) fail with "invalid format option".
	a := 0
	for {
		a = a*10 + int(fmt[*i]-'0')
		*i++
		if !(*i < len(fmt) && packDigit(fmt[*i]) && a <= (packMAXSIZE-9)/10) {
			break
		}
	}
	return a
}

func (L *LState) packGetnumlimit(fmt []byte, i *int, df int) int {
	sz := packGetnum(fmt, i, df)
	if sz > packMAXINTSIZE || sz <= 0 {
		L.errorf("integral size (%d) out of limits [1,%d]", sz, packMAXINTSIZE)
	}
	return sz
}

func (L *LState) packGetoption(h *packHeader, fmt []byte, i *int) (opt, size int) {
	c := fmt[*i]
	*i++
	switch c {
	case 'b':
		return kInt, 1
	case 'B':
		return kUint, 1
	case 'h':
		return kInt, 2
	case 'H':
		return kUint, 2
	case 'l', 'j':
		return kInt, 8
	case 'L', 'J', 'T':
		return kUint, 8
	case 'f':
		return kFloat, 4
	case 'n':
		return kNumber, 8
	case 'd':
		return kDouble, 8
	case 'i':
		return kInt, L.packGetnumlimit(fmt, i, 4)
	case 'I':
		return kUint, L.packGetnumlimit(fmt, i, 4)
	case 's':
		return kString, L.packGetnumlimit(fmt, i, 8)
	case 'c':
		sz := packGetnum(fmt, i, -1)
		if sz == -1 {
			L.errorf("missing size for format option 'c'")
		}
		return kChar, sz
	case 'z':
		return kZstr, 0
	case 'x':
		return kPadding, 1
	case 'X':
		return kPaddalign, 0
	case ' ':
		return kNop, 0
	case '<':
		h.little = true
		return kNop, 0
	case '>':
		h.little = false
		return kNop, 0
	case '=':
		h.little = nativeLittle
		return kNop, 0
	case '!':
		h.maxalign = L.packGetnumlimit(fmt, i, 8)
		return kNop, 0
	default:
		L.errorf("invalid format option '%c'", c)
		return kNop, 0
	}
}

func (L *LState) packGetdetails(h *packHeader, totalsize int, fmt []byte, i *int) (opt, size, ntoalign int) {
	opt, size = L.packGetoption(h, fmt, i)
	align := size
	if opt == kPaddalign {
		if *i >= len(fmt) {
			L.argError(1, "invalid next option for option 'X'")
		}
		var o2 int
		o2, align = L.packGetoption(h, fmt, i)
		if o2 == kChar || align == 0 {
			L.argError(1, "invalid next option for option 'X'")
		}
	}
	if align <= 1 || opt == kChar {
		return opt, size, 0
	}
	if align > h.maxalign {
		align = h.maxalign
	}
	if align&(align-1) != 0 {
		L.argError(1, "format asks for alignment not power of 2")
	}
	ntoalign = (align - (totalsize & (align - 1))) & (align - 1)
	return opt, size, ntoalign
}

func packIdx(i int, little bool, size int) int {
	if little {
		return i
	}
	return size - 1 - i
}

// packint writes n in `size` bytes with the given endianness and sign extension.
func packint(buf []byte, n uint64, little bool, size int, neg bool) []byte {
	tmp := make([]byte, size)
	tmp[packIdx(0, little, size)] = byte(n & 0xFF)
	for i := 1; i < size; i++ {
		n >>= 8
		tmp[packIdx(i, little, size)] = byte(n & 0xFF)
	}
	if neg && size > packSZINT {
		for i := packSZINT; i < size; i++ {
			tmp[packIdx(i, little, size)] = 0xFF
		}
	}
	return append(buf, tmp...)
}

func (L *LState) unpackint(b []byte, little bool, size int, signed bool) int64 {
	var res uint64
	limit := size
	if limit > packSZINT {
		limit = packSZINT
	}
	for i := limit - 1; i >= 0; i-- {
		res <<= 8
		res |= uint64(b[packIdx(i, little, size)])
	}
	if size < packSZINT {
		if signed {
			mask := uint64(1) << uint(size*8-1)
			res = (res ^ mask) - mask
		}
	} else if size > packSZINT {
		var mask byte
		if signed && int64(res) < 0 {
			mask = 0xFF
		}
		for i := limit; i < size; i++ {
			if b[packIdx(i, little, size)] != mask {
				L.errorf("%d-byte integer does not fit into Lua Integer", size)
			}
		}
	}
	return int64(res)
}

func strPack(L *LState) int {
	fmt := []byte(L.checkString(1))
	h := packHeader{little: nativeLittle, maxalign: 1}
	arg := 1
	var buf []byte
	totalsize := 0
	i := 0
	for i < len(fmt) {
		opt, size, ntoalign := L.packGetdetails(&h, totalsize, fmt, &i)
		totalsize += ntoalign + size
		for ; ntoalign > 0; ntoalign-- {
			buf = append(buf, 0)
		}
		arg++
		switch opt {
		case kInt:
			n := L.checkInt(arg)
			if size < packSZINT {
				lim := int64(1) << uint(size*8-1)
				if !(-lim <= n && n < lim) {
					L.argError(arg, "integer overflow")
				}
			}
			buf = packint(buf, uint64(n), h.little, size, n < 0)
		case kUint:
			n := L.checkInt(arg)
			if size < packSZINT {
				if uint64(n) >= (uint64(1) << uint(size*8)) {
					L.argError(arg, "unsigned overflow")
				}
			}
			buf = packint(buf, uint64(n), h.little, size, false)
		case kFloat:
			buf = packint(buf, uint64(math.Float32bits(float32(L.checkNumber(arg)))), h.little, 4, false)
		case kNumber, kDouble:
			buf = packint(buf, math.Float64bits(L.checkNumber(arg)), h.little, 8, false)
		case kChar:
			s := L.checkString(arg)
			if len(s) > size {
				L.argError(arg, "string longer than given size")
			}
			buf = append(buf, s...)
			for k := len(s); k < size; k++ {
				buf = append(buf, 0)
			}
		case kString:
			s := L.checkString(arg)
			if size < packSZSIZET && uint64(len(s)) >= (uint64(1)<<uint(size*8)) {
				L.argError(arg, "string length does not fit in given size")
			}
			buf = packint(buf, uint64(len(s)), h.little, size, false)
			buf = append(buf, s...)
			totalsize += len(s)
		case kZstr:
			s := L.checkString(arg)
			if strings.IndexByte(s, 0) >= 0 {
				L.argError(arg, "string contains zeros")
			}
			buf = append(buf, s...)
			buf = append(buf, 0)
			totalsize += len(s) + 1
		case kPadding:
			buf = append(buf, 0)
			arg--
		case kPaddalign, kNop:
			arg--
		}
	}
	L.Push(MkString(string(buf)))
	return 1
}

func strUnpack(L *LState) int {
	fmt := []byte(L.checkString(1))
	data := L.checkString(2)
	ld := len(data)
	pos := int(strRelIndex(L.optInt(3, 1), int64(ld))) - 1
	if pos < 0 {
		pos = 0
	}
	if pos > ld {
		L.argError(3, "initial position out of string")
	}
	h := packHeader{little: nativeLittle, maxalign: 1}
	n := 0
	i := 0
	for i < len(fmt) {
		opt, size, ntoalign := L.packGetdetails(&h, pos, fmt, &i)
		if ntoalign+size > ld-pos {
			L.argError(2, "data string too short")
		}
		pos += ntoalign
		switch opt {
		case kInt, kUint:
			L.Push(Int(L.unpackint([]byte(data[pos:pos+size]), h.little, size, opt == kInt)))
			n++
		case kFloat:
			bits := uint32(readUintLE(data[pos:pos+4], h.little, 4))
			L.Push(Float(float64(math.Float32frombits(bits))))
			n++
		case kNumber, kDouble:
			bits := readUintLE(data[pos:pos+8], h.little, 8)
			L.Push(Float(math.Float64frombits(bits)))
			n++
		case kChar:
			L.Push(MkString(data[pos : pos+size]))
			n++
		case kString:
			length := int(uint64(L.unpackint([]byte(data[pos:pos+size]), h.little, size, false)))
			if length > ld-pos-size {
				L.argError(2, "data string too short")
			}
			L.Push(MkString(data[pos+size : pos+size+length]))
			pos += length
			n++
		case kZstr:
			end := strings.IndexByte(data[pos:], 0)
			if end < 0 {
				L.argError(2, "unfinished string for format 'z'")
			}
			L.Push(MkString(data[pos : pos+end]))
			pos += end + 1
			n++
		case kPadding, kPaddalign, kNop:
		}
		pos += size
	}
	L.Push(Int(int64(pos + 1)))
	return n + 1
}

func readUintLE(b string, little bool, size int) uint64 {
	var res uint64
	for i := size - 1; i >= 0; i-- {
		res = (res << 8) | uint64(b[packIdx(i, little, size)])
	}
	return res
}

func strPacksize(L *LState) int {
	fmt := []byte(L.checkString(1))
	h := packHeader{little: nativeLittle, maxalign: 1}
	total := 0
	i := 0
	for i < len(fmt) {
		opt, size, ntoalign := L.packGetdetails(&h, total, fmt, &i)
		if opt == kString || opt == kZstr {
			L.argError(1, "variable-length format")
		}
		size += ntoalign // total space used by this option
		if total > packMAXSIZE-size {
			L.argError(1, "format result too large")
		}
		total += size
	}
	L.Push(Int(int64(total)))
	return 1
}
