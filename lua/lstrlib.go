package luapure

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"sync"
	"unsafe"
)

// strDump serializes a Lua function to a binary chunk (string.dump). The second
// argument requests stripping debug info. Only Lua functions can be dumped.
func strDump(L *LState) int {
	fn := L.Arg(1)
	if !fn.IsFunction() {
		L.typeArgError(1, "function")
	}
	cl := fn.closure()
	strip := L.NArgs() >= 2 && !L.Arg(2).IsFalsy()
	if cl == nil || !cl.isLua() {
		L.errorf("unable to dump given function")
	}
	var buf bytes.Buffer
	if err := dumpProto(&buf, cl.proto, strip); err != nil {
		L.errorf("unable to dump given function")
	}
	L.Push(MkString(buf.String()))
	return 1
}

// The string library (lstrlib.c): basic operations, string.format, and the Lua
// pattern matcher (find/match/gmatch/gsub). The string metatable wires __index
// to the library table and installs the arithmetic metamethods that give Lua
// its string->number coercion in arithmetic.

// OpenString installs the string library and the string metatable.
func (L *LState) OpenString() {
	strlib := newTable()
	setFuncs(strlib, map[string]GoFunc{
		"len":      strLen,
		"sub":      strSub,
		"upper":    strUpper,
		"lower":    strLower,
		"rep":      strRep,
		"reverse":  strReverse,
		"byte":     strByte,
		"char":     strChar,
		"format":   strFormat,
		"find":     strFind,
		"match":    strMatch,
		"gmatch":   strGmatch,
		"gsub":     strGsub,
		"pack":     strPack,
		"unpack":   strUnpack,
		"packsize": strPacksize,
		"dump":     strDump,
	})
	L.registerTable("string", strlib)

	// String metatable: __index -> string library, plus arithmetic coercion.
	mt := newTable()
	mt.rawset(MkString("__index"), mkTable(strlib))
	installStringArith(mt)
	L.stringMT = mt
}

// installStringArith mirrors lstrlib.c's string metamethods: ONLY arithmetic
// (__add..__idiv, __unm). Bitwise operators have no string metamethod in 5.4,
// so "5" & 1 errors. Each falls back to the PUC arith()/trymt() path.
func installStringArith(mt *Table) {
	bin := func(op int, mtname string) GoFunc {
		return func(L *LState) int {
			a, ok1 := tonumValue(L.Arg(1))
			b, ok2 := tonumValue(L.Arg(2))
			if ok1 && ok2 {
				res, ok := rawArith(op, a, b)
				if !ok {
					L.errorf("number has no integer representation")
				}
				L.Push(res)
				return 1
			}
			L.strTryMT(mtname) // trymt
			return 1
		}
	}
	mt.rawset(MkString("__add"), NewGoFunc("__add", bin(OpAdd, "__add")))
	mt.rawset(MkString("__sub"), NewGoFunc("__sub", bin(OpSub, "__sub")))
	mt.rawset(MkString("__mul"), NewGoFunc("__mul", bin(OpMul, "__mul")))
	mt.rawset(MkString("__div"), NewGoFunc("__div", bin(OpDiv, "__div")))
	mt.rawset(MkString("__mod"), NewGoFunc("__mod", bin(OpMod, "__mod")))
	mt.rawset(MkString("__pow"), NewGoFunc("__pow", bin(OpPow, "__pow")))
	mt.rawset(MkString("__idiv"), NewGoFunc("__idiv", bin(OpIDiv, "__idiv")))
	// Unary minus: PUC calls __unm with the operand duplicated, so a
	// non-numeric string errors "attempt to unm a 'string' with a 'string'".
	mt.rawset(MkString("__unm"), NewGoFunc("__unm", func(L *LState) int {
		a, ok := tonumValue(L.Arg(1))
		if !ok {
			t := typeName(L.Arg(1))
			L.errorf("attempt to unm a '%s' with a '%s'", t, t)
		}
		res, _ := rawArith(OpUnm, a, a)
		L.Push(res)
		return 1
	}))
}

// strTryMT is lstrlib.c's trymt: with the original two arguments, if the second
// is itself a string or has no such metamethod, raise PUC's typed error;
// otherwise call the second operand's metamethod. Uses raw type names
// (luaL_typename), matching PUC.
func (L *LState) strTryMT(mtname string) {
	a1, a2 := L.Arg(1), L.Arg(2)
	var mm Value = Nil
	if !a2.IsString() {
		if mt := L.metatableOf(a2); mt != nil {
			mm = mt.rawgetStr(mtname)
		}
	}
	if mm.IsNil() {
		L.errorf("attempt to %s a '%s' with a '%s'", mtname[2:], typeName(a1), typeName(a2))
	}
	res := L.CallValue(mm, []Value{a1, a2}, 1)
	if len(res) > 0 {
		L.Push(res[0])
	} else {
		L.Push(Nil)
	}
}

func strLen(L *LState) int {
	L.Push(Int(int64(len(L.checkString(1)))))
	return 1
}

// strRelIndex converts a possibly-negative 1-based string index (luaL_posrelat).
func strRelIndex(pos, l int64) int64 {
	if pos >= 0 {
		return pos
	}
	if -pos > l {
		return 0
	}
	return l + pos + 1
}

func strSub(L *LState) int {
	s := L.checkString(1)
	l := int64(len(s))
	i := strRelIndex(L.checkInt(2), l)
	j := strRelIndex(L.optInt(3, -1), l)
	if i < 1 {
		i = 1
	}
	if j > l {
		j = l
	}
	if i > j {
		L.Push(MkString(""))
		return 1
	}
	L.Push(MkString(s[i-1 : j]))
	return 1
}

func strUpper(L *LState) int {
	L.Push(MkString(strings.ToUpper(L.checkString(1))))
	return 1
}

func strLower(L *LState) int {
	L.Push(MkString(strings.ToLower(L.checkString(1))))
	return 1
}

func strRep(L *LState) int {
	s := L.checkString(1)
	n := L.checkInt(2)
	sep := ""
	if L.NArgs() >= 3 && !L.Arg(3).IsNil() {
		sep = L.checkString(3)
	}
	if n <= 0 {
		L.Push(MkString(""))
		return 1
	}
	// Reject results past MAXSIZE before allocating, exactly as PUC str_rep:
	// otherwise string.rep('aa', 1<<30) would try to build a multi-GB string.
	l, lsep := int64(len(s)), int64(len(sep))
	if l+lsep < l || l+lsep > packMAXSIZE/n {
		L.errorf("resulting string too large")
	}
	var sb strings.Builder
	sb.Grow(int(n*l + (n-1)*lsep))
	for i := int64(0); i < n; i++ {
		if i > 0 {
			sb.WriteString(sep)
		}
		sb.WriteString(s)
	}
	L.Push(MkString(sb.String()))
	return 1
}

func strReverse(L *LState) int {
	s := []byte(L.checkString(1))
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
	L.Push(MkString(string(s)))
	return 1
}

func strByte(L *LState) int {
	s := L.checkString(1)
	l := int64(len(s))
	i := strRelIndex(L.optInt(2, 1), l)
	j := strRelIndex(L.optInt(3, i), l)
	if i < 1 {
		i = 1
	}
	if j > l {
		j = l
	}
	cnt := 0
	for k := i; k <= j; k++ {
		L.Push(Int(int64(s[k-1])))
		cnt++
	}
	return cnt
}

func strChar(L *LState) int {
	n := L.NArgs()
	b := make([]byte, n)
	for i := 1; i <= n; i++ {
		c := L.checkInt(i)
		if c < 0 || c > 255 {
			L.argError(i, "value out of range")
		}
		b[i-1] = byte(c)
	}
	L.Push(MkString(string(b)))
	return 1
}

// Valid flags per conversion (lstrlib.c L_FMTFLAGS*); '0' counts as a flag.
const (
	fmtFlagsF   = "-+#0 "           // a A e E f F g G
	fmtFlagsX   = "-#0"             // o x X
	fmtFlagsI   = "-+0 "            // d i
	fmtFlagsU   = "-0"              // u
	fmtFlagsC   = "-"               // c p s
	fmtFlagsAll = "-+#0 123456789." // getformat span (flags+width+precision)
)

func fmtIsAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// fmtGet2digits skips up to two decimal digits starting at j, like PUC get2digits.
func fmtGet2digits(form string, j int) int {
	if j < len(form) && form[j] >= '0' && form[j] <= '9' {
		j++
		if j < len(form) && form[j] >= '0' && form[j] <= '9' {
			j++
		}
	}
	return j
}

// checkFormat mirrors PUC checkformat: validate that 'form' (a "%...X" spec)
// uses only the flags allowed for its conversion, has at most a two-digit width
// (not starting with '0') and, when permitted, a two-digit precision.
func (L *LState) checkFormat(form, flags string, precision bool) {
	j := 1 // skip '%'
	for j < len(form) && strings.IndexByte(flags, form[j]) >= 0 {
		j++
	}
	if j < len(form) && form[j] != '0' { // a width cannot start with '0'
		j = fmtGet2digits(form, j)
		if j < len(form) && form[j] == '.' && precision {
			j++
			j = fmtGet2digits(form, j)
		}
	}
	if j >= len(form) || !fmtIsAlpha(form[j]) {
		L.errorf("invalid conversion specification: '%s'", form)
	}
}

// ptrCanonTab canonicalizes short strings by content for "%p" display only, so
// equal short strings report the same address (PUC short-string interning) — a
// new backing object is never created on the runtime string path for this.
var ptrCanonTab sync.Map // string -> *luaString

func ptrCanon(s string) unsafe.Pointer {
	if v, ok := ptrCanonTab.Load(s); ok {
		return unsafe.Pointer(v.(*luaString))
	}
	v, _ := ptrCanonTab.LoadOrStore(s, &luaString{s: s})
	return unsafe.Pointer(v.(*luaString))
}

// strFormat ports string.format (str_format in lstrlib.c).
func strFormat(L *LState) int {
	format := L.checkString(1)
	top := L.NArgs()
	var sb strings.Builder
	arg := 1
	i := 0
	for i < len(format) {
		c := format[i]
		if c != '%' {
			sb.WriteByte(c)
			i++
			continue
		}
		i++ // skip '%'
		if i < len(format) && format[i] == '%' {
			sb.WriteByte('%')
			i++
			continue
		}
		// getformat: span flags/width/precision, then the conversion specifier.
		start := i
		for i < len(format) && strings.IndexByte(fmtFlagsAll, format[i]) >= 0 {
			i++
		}
		if i >= len(format) {
			L.errorf("invalid conversion '%%%s' to 'format'", format[start:i])
		}
		verb := format[i]
		form := "%" + format[start:i+1]
		i++
		if len(form) >= 32-10 { // MAX_FORMAT - 10
			L.errorf("invalid format (too long)")
		}
		arg++
		if arg > top {
			L.argError(arg, "no value")
		}
		switch verb {
		case 'd', 'i':
			n := L.checkInt(arg)
			L.checkFormat(form, fmtFlagsI, true)
			sb.WriteString(fmt.Sprintf(form[:len(form)-1]+"d", n))
		case 'u':
			n := L.checkInt(arg)
			L.checkFormat(form, fmtFlagsU, true)
			sb.WriteString(fmt.Sprintf(form[:len(form)-1]+"d", uint64(n)))
		case 'o', 'x', 'X':
			n := L.checkInt(arg)
			L.checkFormat(form, fmtFlagsX, true)
			sb.WriteString(fmt.Sprintf(form, uint64(n)))
		case 'c':
			// C printf '%c' emits a single byte; Go's %c would emit a rune.
			n := L.checkInt(arg)
			L.checkFormat(form, fmtFlagsC, false)
			sb.WriteString(fmt.Sprintf(form[:len(form)-1]+"s", string([]byte{byte(n)})))
		case 'a', 'A':
			f := L.checkNumber(arg)
			L.checkFormat(form, fmtFlagsF, true)
			if s, ok := formatSpecialFloat(form, f, verb == 'A'); ok {
				sb.WriteString(s)
				break
			}
			letter := byte('x')
			if verb == 'A' {
				letter = 'X'
			}
			sb.WriteString(fmtFixHexExp(fmt.Sprintf(form[:len(form)-1]+string(letter), f)))
		case 'e', 'E', 'f', 'g', 'G': // PUC string.format has no %F (not in C89)
			f := L.checkNumber(arg)
			L.checkFormat(form, fmtFlagsF, true)
			// C's '%g' defaults to 6 significant digits; Go's defaults to the
			// shortest unique form, so inject the C default when none is given
			// ('%e'/'%f' already share C's default of 6).
			if (verb == 'g' || verb == 'G') && !strings.Contains(form, ".") {
				form = form[:len(form)-1] + ".6" + string(verb)
			}
			if s, ok := formatSpecialFloat(form, f, verb >= 'A' && verb <= 'Z'); ok {
				sb.WriteString(s)
				break
			}
			sb.WriteString(fmt.Sprintf(form, f))
		case 's':
			s := L.tostring(L.Arg(arg))
			if len(form) > 2 { // has modifiers
				L.checkFormat(form, fmtFlagsC, true)
				// PUC: with a width/precision, the C formatter can't carry an
				// embedded \0, so reject it ("string contains zeros").
				if strings.IndexByte(s, 0) >= 0 {
					L.argError(arg, "string contains zeros")
				}
			}
			sb.WriteString(fmt.Sprintf(form, s))
		case 'p':
			// lua_topointer is NULL for non-reference values; PUC then formats
			// the literal "(null)" as a string, honouring width either way.
			L.checkFormat(form, fmtFlagsC, false)
			v := L.Arg(arg)
			switch {
			case v.IsString():
				// PUC interns short strings, so equal short strings share an
				// address; we canonicalize by content here (the only point string
				// identity is observable), keeping it off the runtime hot path.
				ptr := v.gc
				if len(v.Str()) <= maxShortLen {
					ptr = ptrCanon(v.Str())
				}
				sb.WriteString(fmt.Sprintf(form, ptr))
			case v.gc != nil:
				sb.WriteString(fmt.Sprintf(form, v.gc))
			default:
				sb.WriteString(fmt.Sprintf(form[:len(form)-1]+"s", "(null)"))
			}
		case 'q':
			if len(form) != 2 { // any modifier present
				L.errorf("specifier '%%q' cannot have modifiers")
			}
			sb.WriteString(L.addLiteral(arg, L.Arg(arg)))
		default:
			L.errorf("invalid conversion '%s' to 'format'", form)
		}
	}
	L.Push(MkString(sb.String()))
	return 1
}

// addLiteral renders a value as a Lua literal for "%q" (addliteral/addquoted in
// lstrlib.c): strings are escaped, integers/floats use a round-trippable
// numeral, and nil/booleans use their keyword.
func (L *LState) addLiteral(arg int, v Value) string {
	switch {
	case v.IsString():
		return fmtAddQuoted(v.Str())
	case v.IsInt():
		n := v.AsInt()
		if n == math.MinInt64 { // numeral would overflow; emit hexadecimal
			return fmt.Sprintf("0x%x", uint64(n))
		}
		return numToString(v)
	case v.IsFloat():
		return fmtQuoteFloat(v.AsFloat())
	case v.IsNil():
		return "nil"
	case v.IsBool():
		if v.AsBool() {
			return "true"
		}
		return "false"
	default:
		L.argError(arg, "value has no literal form")
		return ""
	}
}

func fmtIsCntrl(c byte) bool { return c < 0x20 || c == 0x7f }

// fmtAddQuoted escapes a string so it reads back as the same Lua string literal.
func fmtAddQuoted(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for j := 0; j < len(s); j++ {
		c := s[j]
		switch {
		case c == '"' || c == '\\' || c == '\n':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		case fmtIsCntrl(c):
			// Use \ddd; pad to three digits only when a digit follows, so the
			// escape boundary stays unambiguous.
			if j+1 < len(s) && s[j+1] >= '0' && s[j+1] <= '9' {
				fmt.Fprintf(&sb, "\\%03d", c)
			} else {
				fmt.Fprintf(&sb, "\\%d", c)
			}
		default:
			sb.WriteByte(c)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// fmtQuoteFloat serialises a float so Lua can scan it back (quotefloat).
func fmtQuoteFloat(n float64) string {
	switch {
	case math.IsInf(n, 1):
		return "1e9999"
	case math.IsInf(n, -1):
		return "-1e9999"
	case math.IsNaN(n):
		return "(0/0)"
	default:
		return fmtFixHexExp(fmt.Sprintf("%x", n))
	}
}

// fmtFixHexExp trims the leading zeros C printf omits from a hex-float binary
// exponent: Go prints "0x1p+00" where PUC prints "0x1p+0".
// formatSpecialFloat renders an infinity or NaN the way C printf does — "inf"/
// "nan" (or "INF"/"NAN" for an uppercase verb) with the sign from signbit and
// the +/space flags, padded to the format's width. Go's fmt would emit "+Inf"/
// "NaN" instead. Returns ok=false for finite values.
func formatSpecialFloat(form string, f float64, upper bool) (string, bool) {
	if !math.IsInf(f, 0) && !math.IsNaN(f) {
		return "", false
	}
	i := 1 // skip '%'
	flags := ""
	for i < len(form) && strings.IndexByte("-+ #0", form[i]) >= 0 {
		flags += string(form[i])
		i++
	}
	width := 0
	for i < len(form) && form[i] >= '0' && form[i] <= '9' {
		width = width*10 + int(form[i]-'0')
		i++
	}
	body := "inf"
	if math.IsNaN(f) {
		body = "nan"
	}
	if upper {
		body = strings.ToUpper(body)
	}
	sign := ""
	switch {
	case math.Signbit(f):
		sign = "-"
	case strings.Contains(flags, "+"):
		sign = "+"
	case strings.Contains(flags, " "):
		sign = " "
	}
	s := sign + body
	if n := width - len(s); n > 0 {
		pad := strings.Repeat(" ", n)
		if strings.Contains(flags, "-") {
			s += pad
		} else {
			s = pad + s
		}
	}
	return s, true
}

func fmtFixHexExp(s string) string {
	idx := strings.IndexAny(s, "pP")
	if idx < 0 {
		return s
	}
	mant, exp := s[:idx+1], s[idx+1:]
	sign := ""
	if len(exp) > 0 && (exp[0] == '+' || exp[0] == '-') {
		sign, exp = exp[:1], exp[1:]
	}
	exp = strings.TrimLeft(exp, "0")
	if exp == "" {
		exp = "0"
	}
	return mant + sign + exp
}
