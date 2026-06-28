package luapure

import (
	"strconv"
	"strings"
)

// parseNumeral converts a Lua numeral literal (the verbatim text the lexer kept
// in ast.NumberExpr) into an integer or float Value, following Lua 5.4's rules:
// a literal with a radix point or exponent is a float; a hex integer wraps
// modulo 2^64; a decimal integer that overflows int64 falls back to float.
// Returns ok=false on a malformed numeral.
func parseNumeral(s string) (Value, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Value{}, false
	}
	body := s
	if body[0] == '+' || body[0] == '-' {
		body = body[1:]
	}
	lower := strings.ToLower(body)
	if strings.HasPrefix(lower, "0x") {
		if strings.ContainsAny(lower, ".p") { // hex float
			hf := s
			if !strings.ContainsRune(lower, 'p') {
				hf += "p0" // Go requires a binary exponent; Lua allows omitting it
			}
			if f, ok := parseFloatLua(hf); ok {
				return Float(f), true
			}
			return Value{}, false
		}
		if i, ok := parseHexIntWrap(s); ok {
			return Int(i), true
		}
		return Value{}, false
	}
	if strings.ContainsAny(lower, ".e") { // decimal float
		if f, ok := parseFloatLua(s); ok {
			return Float(f), true
		}
		return Value{}, false
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil { // decimal integer
		return Int(i), true
	}
	if f, ok := parseFloatLua(s); ok { // overflow -> float
		return Float(f), true
	}
	return Value{}, false
}

// parseFloatLua parses a float the way PUC's strtod-based l_str2d does: a
// magnitude that overflows yields ±Inf and one that underflows yields 0 — both
// are valid results, not errors. Go's strconv.ParseFloat returns those exact
// clamped values together with ErrRange, so accept ErrRange while rejecting
// genuine syntax errors (ErrSyntax).
func parseFloatLua(s string) (float64, bool) {
	f, err := strconv.ParseFloat(s, 64)
	if err == nil {
		return f, true
	}
	if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
		return f, true
	}
	return 0, false
}

// parseHexIntWrap parses a hex integer literal (with optional sign and "0x"
// prefix), wrapping modulo 2^64 as Lua does for hex integer constants.
func parseHexIntWrap(s string) (int64, bool) {
	neg := false
	switch {
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	case strings.HasPrefix(s, "-"):
		neg = true
		s = s[1:]
	}
	if len(s) < 3 || (s[0] != '0') || (s[1] != 'x' && s[1] != 'X') {
		return 0, false
	}
	digits := s[2:]
	if digits == "" {
		return 0, false
	}
	var acc uint64
	for i := 0; i < len(digits); i++ {
		d := hexDigit(digits[i])
		if d < 0 {
			return 0, false
		}
		acc = acc*16 + uint64(d) // overflow wraps, matching Lua
	}
	if neg {
		acc = -acc
	}
	return int64(acc), true
}

func hexDigit(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}
