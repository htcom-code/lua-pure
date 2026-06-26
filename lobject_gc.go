package luapure

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unsafe"
)

// This file extends the tagged-struct Value (value.go) with the garbage-
// collected runtime objects the VM needs — tables and closures — plus the
// number<->string coercions PUC-Lua applies throughout lvm.c/lobject.c
// (luaO_str2num, luaO_tostring, luaV_tonumber_). Scalars stay inline; GC
// objects ride in the Value.gc pointer (GC-safe, since Go scans it).

// --- GC-object constructors / accessors ---

func mkTable(t *Table) Value { return Value{tag: tagTable, gc: unsafe.Pointer(t)} }

// tablev returns the *Table payload; valid only when v.tag == tagTable.
func (v Value) tablev() *Table { return (*Table)(v.gc) }

func mkClosure(c *Closure) Value { return Value{tag: tagFunction, gc: unsafe.Pointer(c)} }

// closure returns the *Closure payload; valid only when v.tag == tagFunction.
func (v Value) closure() *Closure { return (*Closure)(v.gc) }

// userData is the backing object for full userdata: an arbitrary Go payload
// plus its own metatable (file handles, etc.).
type userData struct {
	data   interface{}
	meta   *Table
	finReg bool // a Go finalizer for __gc has been attached (PUC FINALIZEDBIT)
}

func mkUserData(u *userData) Value { return Value{tag: tagUserData, gc: unsafe.Pointer(u)} }

// mkLightUserData wraps a bare pointer as light userdata, compared by identity
// (used by debug.upvalueid to hand out a stable per-upvalue token).
func mkLightUserData(p unsafe.Pointer) Value {
	return Value{tag: tagLightUserData, gc: p}
}

func (v Value) userData() *userData { return (*userData)(v.gc) }

func (v Value) IsTable() bool    { return v.tag == tagTable }
func (v Value) IsFunction() bool { return v.tag == tagFunction }
func (v Value) IsUserData() bool { return v.tag == tagUserData }
func (v Value) IsThread() bool   { return v.tag == tagThread }

func mkThread(t *LState) Value { return Value{tag: tagThread, gc: unsafe.Pointer(t)} }

// threadv returns the *LState payload; valid only when v.tag == tagThread.
func (v Value) threadv() *LState { return (*LState)(v.gc) }

// --- type names (ltm.c luaT_typenames_ / luaT_objtypename) ---

func typeName(v Value) string {
	switch v.tag {
	case tagNil:
		return "nil"
	case tagFalse, tagTrue:
		return "boolean"
	case tagInt, tagFloat:
		return "number"
	case tagString:
		return "string"
	case tagTable:
		return "table"
	case tagFunction:
		return "function"
	case tagUserData:
		return "userdata"
	case tagThread:
		return "thread"
	case tagLightUserData:
		return "userdata"
	}
	return "no value"
}

// --- number -> string (lobject.c tostringbuff / luaO_tostring) ---

// numToString renders a number Value exactly as PUC's tostringbuff: integers
// with "%d", floats with "%.14g" plus a trailing ".0" when the result would
// otherwise look like an integer. inf/nan match printf's lowercase spelling.
func numToString(v Value) string {
	if v.IsInt() {
		return strconv.FormatInt(v.AsInt(), 10)
	}
	f := v.AsFloat()
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		return "nan"
	}
	s := fmt.Sprintf("%.14g", f)
	// PUC appends ".0" when the text is all sign+digits (looks like an int).
	if strings.IndexFunc(s, func(r rune) bool {
		return r != '-' && (r < '0' || r > '9')
	}) < 0 {
		s += ".0"
	}
	return s
}

// --- string -> number (lobject.c luaO_str2num) ---

// str2num converts a numeric string to an integer Value if possible, else a
// float Value, matching luaO_str2num (l_str2int then l_str2d). ok=false means
// the whole string is not a valid numeral.
func str2num(s string) (Value, bool) {
	if i, ok := str2int(s); ok {
		return Int(i), true
	}
	if f, ok := str2float(s); ok {
		return Float(f), true
	}
	return Value{}, false
}

// str2int ports l_str2int: optional sign, decimal or 0x-hex digits, surrounding
// spaces allowed; decimal overflow rejects (so the float path can try).
func str2int(s string) (int64, bool) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, false
	}
	neg := false
	switch t[0] {
	case '-':
		neg = true
		t = t[1:]
	case '+':
		t = t[1:]
	}
	if t == "" {
		return 0, false
	}
	var a uint64
	empty := true
	if len(t) >= 2 && t[0] == '0' && (t[1] == 'x' || t[1] == 'X') {
		t = t[2:]
		for i := 0; i < len(t); i++ {
			c := t[i]
			var d uint64
			switch {
			case c >= '0' && c <= '9':
				d = uint64(c - '0')
			case c >= 'a' && c <= 'f':
				d = uint64(c-'a') + 10
			case c >= 'A' && c <= 'F':
				d = uint64(c-'A') + 10
			default:
				return 0, false // trailing junk (hex floats handled by str2float)
			}
			a = a*16 + d // wraps, like PUC's unsigned accumulation
			empty = false
		}
	} else {
		const maxBy10 = uint64(math.MaxInt64) / 10
		const maxLast = int(math.MaxInt64 % 10)
		ni := 0
		if neg {
			ni = 1
		}
		for i := 0; i < len(t); i++ {
			c := t[i]
			if c < '0' || c > '9' {
				return 0, false
			}
			d := int(c - '0')
			if a >= maxBy10 && (a > maxBy10 || d > maxLast+ni) {
				return 0, false // overflow: reject as integer
			}
			a = a*10 + uint64(d)
			empty = false
		}
	}
	if empty {
		return 0, false
	}
	if neg {
		return int64(0 - a), true
	}
	return int64(a), true
}

// str2float ports l_str2d: a Lua float numeral, rejecting inf/nan spellings.
func str2float(s string) (float64, bool) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, false
	}
	low := strings.ToLower(t)
	if strings.Contains(low, "inf") || strings.Contains(low, "nan") {
		return 0, false // PUC rejects 'inf'/'nan' from string coercion
	}
	// Lua accepts hex floats without a binary exponent ("0x1.8"); Go's
	// ParseFloat requires one, so synthesise "p0" when it is missing.
	if strings.Contains(low, "0x") && !strings.Contains(low, "p") {
		t += "p0"
	}
	// strtod (and thus PUC) yields ±Inf on overflow and 0 on underflow rather
	// than failing; parseFloatLua mirrors that by accepting ErrRange.
	return parseFloatLua(t)
}

// --- value -> number / integer coercions used by the VM ---

// tonumberCvt converts v to a float, following 'tonumber' macro semantics:
// numbers convert directly, strings via str2num. Used where the VM needs a
// float (for arithmetic on mixed operands).
func tonumberCvt(v Value) (float64, bool) {
	switch {
	case v.IsFloat():
		return v.AsFloat(), true
	case v.IsInt():
		return float64(v.AsInt()), true
	case v.IsString():
		if n, ok := str2num(v.Str()); ok {
			return toNumberNS(n)
		}
	}
	return 0, false
}

// tointegerCvt converts v to an integer (F2Ieq), allowing string coercion.
func tointegerCvt(v Value) (int64, bool) {
	if v.IsString() {
		if n, ok := str2num(v.Str()); ok {
			return toIntegerNS(n)
		}
		return 0, false
	}
	return toIntegerNS(v)
}

// tonumValue returns v itself if it is a number, or the number a numeric
// string coerces to; ok=false otherwise. Mirrors PUC's cvt2num path used by
// the arithmetic opcodes before falling back to metamethods.
func tonumValue(v Value) (Value, bool) {
	if v.IsNumber() {
		return v, true
	}
	if v.IsString() {
		return str2num(v.Str())
	}
	return Value{}, false
}
