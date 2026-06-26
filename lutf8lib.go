package luapure

import "strings"

// The utf8 library (lutf8lib.c): UTF-8 (de)coding with Lua's lax 1–6 byte
// range, plus char/codepoint/len/offset/codes and the charpattern constant.

const (
	maxUnicode = 0x10FFFF
	maxUTF     = 0x7FFFFFFF
)

func (L *LState) OpenUTF8() {
	t := newTable()
	setFuncs(t, map[string]GoFunc{
		"char":      utf8Char,
		"codepoint": utf8Codepoint,
		"len":       utf8Len,
		"offset":    utf8Offset,
		"codes":     utf8Codes,
	})
	// charpattern matches one UTF-8 byte sequence.
	t.rawset(MkString("charpattern"), MkString("[\x00-\x7F\xC2-\xFD][\x80-\xBF]*"))
	L.registerTable("utf8", t)
}

// utf8esc encodes codepoint x into 1..6 UTF-8 bytes (lobject.c luaO_utf8esc).
func utf8esc(x uint32) []byte {
	const sz = 8
	var buff [sz]byte
	n := 1
	if x < 0x80 {
		buff[sz-1] = byte(x)
	} else {
		mfb := uint32(0x3f)
		for {
			buff[sz-n] = byte(0x80 | (x & 0x3f))
			n++
			x >>= 6
			mfb >>= 1
			if x <= mfb {
				break
			}
		}
		buff[sz-n] = byte((^mfb << 1) | x)
	}
	return buff[sz-n:]
}

var utf8limits = [6]uint32{^uint32(0), 0x80, 0x800, 0x10000, 0x200000, 0x4000000}

// utf8Decode decodes one sequence starting at s[i], returning the codepoint and
// the index just past it; ok=false on an invalid sequence (lutf8lib.c).
func utf8Decode(s string, i int, strict bool) (val uint32, next int, ok bool) {
	c := uint32(s[i])
	var res uint32
	if c < 0x80 {
		res = c
	} else {
		count := 0
		for c&0x40 != 0 {
			count++
			if i+count >= len(s) {
				return 0, 0, false
			}
			cc := uint32(s[i+count])
			if cc&0xC0 != 0x80 {
				return 0, 0, false
			}
			res = (res << 6) | (cc & 0x3F)
			c <<= 1
		}
		res |= (c & 0x7F) << uint(count*5)
		if count > 5 || res > maxUTF || res < utf8limits[count] {
			return 0, 0, false
		}
		i += count
	}
	if strict {
		if res > maxUnicode || (0xD800 <= res && res <= 0xDFFF) {
			return 0, 0, false
		}
	}
	return res, i + 1, true
}

func utf8Char(L *LState) int {
	n := L.NArgs()
	var sb strings.Builder
	for i := 1; i <= n; i++ {
		c := L.checkInt(i)
		if c < 0 || c > maxUTF {
			L.argError(i, "value out of range")
		}
		sb.Write(utf8esc(uint32(c)))
	}
	L.Push(MkString(sb.String()))
	return 1
}

func utf8Codepoint(L *LState) int {
	s := L.checkString(1)
	ls := int64(len(s))
	i := strRelIndex(L.optInt(2, 1), ls)
	j := strRelIndex(L.optInt(3, i), ls)
	lax := L.NArgs() >= 4 && !L.Arg(4).IsFalsy()
	if i < 1 {
		L.argError(2, "out of bounds")
	}
	if j > ls {
		L.argError(3, "out of bounds")
	}
	if j-i >= 0x7FFFFFFF { // (lua_Integer -> int) overflow guard
		L.errorf("string slice too long")
	}
	cnt := 0
	pos := int(i - 1)
	for pos < int(j) {
		val, next, ok := utf8Decode(s, pos, !lax)
		if !ok {
			L.errorf("invalid UTF-8 code")
		}
		L.Push(Int(int64(val)))
		cnt++
		pos = next
	}
	return cnt
}

func utf8Len(L *LState) int {
	s := L.checkString(1)
	ls := int64(len(s))
	i := strRelIndex(L.optInt(2, 1), ls)
	j := strRelIndex(L.optInt(3, -1), ls)
	lax := L.NArgs() >= 4 && !L.Arg(4).IsFalsy()
	if i < 1 || i > ls+1 {
		L.argError(2, "initial position out of bounds")
	}
	if j > ls {
		L.argError(3, "final position out of bounds")
	}
	pos := int(i - 1)
	n := 0
	for pos < int(j) {
		_, next, ok := utf8Decode(s, pos, !lax)
		if !ok {
			L.Push(Nil)
			L.Push(Int(int64(pos + 1)))
			return 2
		}
		pos = next
		n++
	}
	L.Push(Int(int64(n)))
	return 1
}

func utf8Offset(L *LState) int {
	s := L.checkString(1)
	ls := len(s)
	n := L.checkInt(2)
	var i int64
	if n >= 0 {
		i = 1
	} else {
		i = int64(ls) + 1
	}
	i = strRelIndex(L.optInt(3, i), int64(ls))
	pos := int(i - 1)
	if pos < 0 || pos > ls {
		L.argError(3, "position out of bounds")
	}
	iscont := func(p int) bool { return p < ls && s[p]&0xC0 == 0x80 }
	if n == 0 {
		for pos > 0 && iscont(pos) {
			pos--
		}
		L.Push(Int(int64(pos + 1)))
		return 1
	}
	if pos < ls && iscont(pos) {
		L.errorf("initial position is a continuation byte")
	}
	if n > 0 {
		n--
		for n > 0 && pos < ls {
			pos++
			for iscont(pos) {
				pos++
			}
			n--
		}
		if n > 0 {
			L.Push(Nil)
			return 1
		}
		L.Push(Int(int64(pos + 1)))
		return 1
	}
	for n < 0 && pos > 0 {
		pos--
		for pos > 0 && iscont(pos) {
			pos--
		}
		n++
	}
	if n < 0 {
		L.Push(Nil)
		return 1
	}
	L.Push(Int(int64(pos + 1)))
	return 1
}

func utf8Codes(L *LState) int {
	s := L.checkString(1)
	// codes(s [, lax]); the iterator decodes strictly unless lax is truthy.
	lax := L.NArgs() >= 2 && !L.Arg(2).IsFalsy()
	strict := !lax
	// The first byte must not be a continuation byte (iter_codes argcheck).
	if len(s) > 0 && s[0]&0xC0 == 0x80 {
		L.argError(1, "invalid UTF-8 code")
	}
	// iter_aux: the control value is the previously returned 1-based index,
	// reused as a 0-based offset; skip continuation bytes to the next char,
	// then decode it (rejecting a stray continuation byte after it).
	iter := func(L *LState) int {
		str := L.Arg(1).Str()
		n := int(L.checkInt(2))
		ls := len(str)
		if n < 0 { // matches PUC's unsigned cast: negative -> done
			return 0
		}
		iscont := func(p int) bool { return p < ls && str[p]&0xC0 == 0x80 }
		if n < ls {
			for iscont(n) {
				n++
			}
		}
		if n >= ls {
			return 0
		}
		code, next, ok := utf8Decode(str, n, strict)
		if !ok || iscont(next) {
			L.errorf("invalid UTF-8 code")
		}
		L.Push(Int(int64(n + 1)))
		L.Push(Int(int64(code)))
		return 2
	}
	L.Push(NewGoFunc("utf8_codes_iter", iter))
	L.Push(MkString(s))
	L.Push(Int(0))
	return 3
}
