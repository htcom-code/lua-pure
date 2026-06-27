package luapure

import "strings"

// Lua pattern matching (lstrlib.c match engine), ported to operate on byte
// slices with integer indices. Captures track an init offset and a length, with
// the same CAP_UNFINISHED / CAP_POSITION sentinels as PUC.

const (
	capUnfinished = -1
	capPosition   = -2
	maxMatchDepth = 200
	// maxCaptures (PUC LUA_MAXCAPTURES) is in luaconf.go; it sizes the fixed
	// capture array below.
)

type capture struct {
	init int // offset into src
	len  int // length, or capUnfinished / capPosition
}

type matchState struct {
	L       *LState
	src     []byte
	pat     []byte
	level   int
	depth   int
	capture [maxCaptures]capture
}

func classEnd(ms *matchState, p int) int {
	c := ms.pat[p]
	p++
	if c == '%' {
		if p >= len(ms.pat) {
			ms.L.errorf("malformed pattern (ends with '%%')")
		}
		return p + 1
	}
	if c == '[' {
		if p < len(ms.pat) && ms.pat[p] == '^' {
			p++
		}
		for {
			if p >= len(ms.pat) {
				ms.L.errorf("malformed pattern (missing ']')")
			}
			cc := ms.pat[p]
			p++
			if cc == '%' && p < len(ms.pat) {
				p++
			}
			if p < len(ms.pat) && ms.pat[p] == ']' {
				return p + 1
			}
			if p >= len(ms.pat) {
				ms.L.errorf("malformed pattern (missing ']')")
			}
		}
	}
	return p
}

func matchClass(c byte, cl byte) bool {
	var res bool
	lower := cl | 0x20 // tolower for ASCII letters
	switch lower {
	case 'a':
		res = isAlpha(c)
	case 'c':
		res = isCntrl(c)
	case 'd':
		res = c >= '0' && c <= '9'
	case 'g':
		res = c > 32 && c < 127
	case 'l':
		res = c >= 'a' && c <= 'z'
	case 'p':
		res = isPunct(c)
	case 's':
		res = isSpace(c)
	case 'u':
		res = c >= 'A' && c <= 'Z'
	case 'w':
		res = isAlpha(c) || (c >= '0' && c <= '9')
	case 'x':
		res = isXdigit(c)
	case 'z':
		res = c == 0
	default:
		return cl == c
	}
	if cl >= 'A' && cl <= 'Z' { // uppercase class negates
		return !res
	}
	return res
}

func isAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isCntrl(c byte) bool { return c < 32 || c == 127 }
func isSpace(c byte) bool { return c == ' ' || (c >= 9 && c <= 13) }
func isXdigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c|0x20 >= 'a' && c|0x20 <= 'f')
}
func isPunct(c byte) bool {
	return c > 32 && c < 127 && !isAlpha(c) && !(c >= '0' && c <= '9')
}

// matchBracketClass tests c against a [...] set spanning pat[p..ec].
func matchBracketClass(ms *matchState, c byte, p, ec int) bool {
	sig := true
	if ms.pat[p+1] == '^' {
		sig = false
		p++
	}
	for p++; p < ec; p++ {
		if ms.pat[p] == '%' {
			p++
			if matchClass(c, ms.pat[p]) {
				return sig
			}
		} else if p+1 < ec && ms.pat[p+1] == '-' && p+2 < ec {
			if ms.pat[p] <= c && c <= ms.pat[p+2] {
				return sig
			}
			p += 2
		} else if ms.pat[p] == c {
			return sig
		}
	}
	return !sig
}

func singleMatch(ms *matchState, s, p, ep int) bool {
	if s >= len(ms.src) {
		return false
	}
	c := ms.src[s]
	switch ms.pat[p] {
	case '.':
		return true
	case '%':
		return matchClass(c, ms.pat[p+1])
	case '[':
		return matchBracketClass(ms, c, p, ep-1)
	default:
		return ms.pat[p] == c
	}
}

func matchBalance(ms *matchState, s, p int) int {
	if p >= len(ms.pat)-1 {
		ms.L.errorf("malformed pattern (missing arguments to '%%b')")
	}
	if s >= len(ms.src) || ms.src[s] != ms.pat[p] {
		return -1
	}
	b, e := ms.pat[p], ms.pat[p+1]
	cont := 1
	for s++; s < len(ms.src); s++ {
		if ms.src[s] == e {
			cont--
			if cont == 0 {
				return s + 1
			}
		} else if ms.src[s] == b {
			cont++
		}
	}
	return -1
}

func maxExpand(ms *matchState, s, p, ep int) int {
	i := 0
	for singleMatch(ms, s+i, p, ep) {
		i++
	}
	for i >= 0 {
		if res := ms.match(s+i, ep+1); res != -1 {
			return res
		}
		i--
	}
	return -1
}

func minExpand(ms *matchState, s, p, ep int) int {
	for {
		if res := ms.match(s, ep+1); res != -1 {
			return res
		}
		if singleMatch(ms, s, p, ep) {
			s++
		} else {
			return -1
		}
	}
}

func startCapture(ms *matchState, s, p, what int) int {
	level := ms.level
	if level >= maxCaptures {
		ms.L.errorf("too many captures")
	}
	ms.capture[level].init = s
	ms.capture[level].len = what
	ms.level = level + 1
	res := ms.match(s, p)
	if res == -1 {
		ms.level--
	}
	return res
}

func endCapture(ms *matchState, s, p int) int {
	l := -1
	for i := ms.level - 1; i >= 0; i-- {
		if ms.capture[i].len == capUnfinished {
			l = i
			break
		}
	}
	if l < 0 {
		ms.L.errorf("invalid pattern capture")
	}
	ms.capture[l].len = s - ms.capture[l].init
	res := ms.match(s, p)
	if res == -1 {
		ms.capture[l].len = capUnfinished
	}
	return res
}

func matchCapture(ms *matchState, s, l int) int {
	l -= '1'
	if l < 0 || l >= ms.level || ms.capture[l].len == capUnfinished {
		ms.L.errorf("invalid capture index %%%d", l+1)
	}
	clen := ms.capture[l].len
	if len(ms.src)-s >= clen && string(ms.src[ms.capture[l].init:ms.capture[l].init+clen]) == string(ms.src[s:s+clen]) {
		return s + clen
	}
	return -1
}

// match returns the end offset of a match starting at s against pattern p, or
// -1 on failure. It mirrors lstrlib.c's match: a single call loops over the
// pattern (PUC's "goto init") so literal sequences do not recurse; only the
// suffix operators and captures recurse, bounded by the matchdepth guard.
func (ms *matchState) match(s, p int) int {
	ms.depth--
	if ms.depth == 0 {
		ms.L.errorf("pattern too complex")
	}
	defer func() { ms.depth++ }()
	for p != len(ms.pat) {
		c := ms.pat[p]
		switch {
		case c == '(':
			if p+1 < len(ms.pat) && ms.pat[p+1] == ')' {
				return startCapture(ms, s, p+2, capPosition)
			}
			return startCapture(ms, s, p+1, capUnfinished)
		case c == ')':
			return endCapture(ms, s, p+1)
		case c == '$' && p+1 == len(ms.pat):
			if s == len(ms.src) {
				return s
			}
			return -1
		case c == '%' && p+1 < len(ms.pat) && ms.pat[p+1] == 'b':
			s = matchBalance(ms, s, p+2)
			if s == -1 {
				return -1
			}
			p += 4
			continue
		case c == '%' && p+1 < len(ms.pat) && ms.pat[p+1] == 'f':
			p += 2
			if p >= len(ms.pat) || ms.pat[p] != '[' {
				ms.L.errorf("missing '[' after '%%f' in pattern")
			}
			ep := classEnd(ms, p)
			var prev byte
			if s != 0 {
				prev = ms.src[s-1]
			}
			var cur byte
			if s < len(ms.src) {
				cur = ms.src[s]
			}
			if !matchBracketClass(ms, prev, p, ep-1) && matchBracketClass(ms, cur, p, ep-1) {
				p = ep
				continue
			}
			return -1
		case c == '%' && p+1 < len(ms.pat) && ms.pat[p+1] >= '0' && ms.pat[p+1] <= '9':
			s = matchCapture(ms, s, int(ms.pat[p+1]))
			if s == -1 {
				return -1
			}
			p += 2
			continue
		}
		// default: a single pattern class plus an optional suffix.
		ep := classEnd(ms, p)
		if !singleMatch(ms, s, p, ep) {
			if ep < len(ms.pat) && (ms.pat[ep] == '*' || ms.pat[ep] == '?' || ms.pat[ep] == '-') {
				p = ep + 1
				continue // accept empty; re-check p against pattern end
			}
			return -1
		}
		if ep < len(ms.pat) {
			switch ms.pat[ep] {
			case '?':
				if res := ms.match(s+1, ep+1); res != -1 {
					return res
				}
				p = ep + 1
				continue
			case '+':
				return maxExpand(ms, s+1, p, ep)
			case '*':
				return maxExpand(ms, s, p, ep)
			case '-':
				return minExpand(ms, s, p, ep)
			}
		}
		s++
		p = ep
	}
	return s
}

// pushCaptures appends the captures (or whole match) of a successful match.
func (ms *matchState) pushCaptures(s, e int, whole bool) []Value {
	n := ms.level
	if (n == 0) && whole {
		n = 1
	}
	out := make([]Value, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, ms.oneCapture(i, s, e))
	}
	return out
}

func (ms *matchState) oneCapture(i, s, e int) Value {
	if i >= ms.level {
		if i != 0 {
			ms.L.errorf("invalid capture index %%%d", i+1)
		}
		return MkString(string(ms.src[s:e]))
	}
	cl := ms.capture[i].len
	if cl == capUnfinished {
		ms.L.errorf("unfinished capture")
	}
	if cl == capPosition {
		return Int(int64(ms.capture[i].init + 1))
	}
	return MkString(string(ms.src[ms.capture[i].init : ms.capture[i].init+cl]))
}

func nospecials(p string) bool {
	return !strings.ContainsAny(p, "^$*+?.([%-")
}

func newMatchState(L *LState, s, p string) *matchState {
	return &matchState{L: L, src: []byte(s), pat: []byte(p), depth: maxMatchDepth}
}

// --- the four pattern functions ---

func strFind(L *LState) int { return strFindAux(L, true) }
func strMatch(L *LState) int { return strFindAux(L, false) }

func strFindAux(L *LState, find bool) int {
	s := L.checkString(1)
	p := L.checkString(2)
	ls := int64(len(s))
	init := strRelIndex(L.optInt(3, 1), ls) - 1
	if init < 0 {
		init = 0
	}
	if init > ls {
		L.Push(Nil)
		return 1
	}
	plain := L.NArgs() >= 4 && !L.Arg(4).IsFalsy()
	if find && (plain || nospecials(p)) {
		if idx := strings.Index(s[init:], p); idx >= 0 {
			start := init + int64(idx)
			L.Push(Int(start + 1))
			L.Push(Int(start + int64(len(p))))
			return 2
		}
		L.Push(Nil)
		return 1
	}
	ms := newMatchState(L, s, p)
	anchor := false
	pp := 0
	if len(ms.pat) > 0 && ms.pat[0] == '^' {
		anchor = true
		pp = 1
	}
	s1 := int(init)
	for {
		ms.level = 0
		ms.depth = maxMatchDepth
		if e := ms.match(s1, pp); e != -1 {
			if find {
				L.Push(Int(int64(s1) + 1))
				L.Push(Int(int64(e)))
				caps := ms.pushCaptures(s1, e, false)
				for _, c := range caps {
					L.Push(c)
				}
				return 2 + len(caps)
			}
			caps := ms.pushCaptures(s1, e, true)
			for _, c := range caps {
				L.Push(c)
			}
			return len(caps)
		}
		s1++
		if s1 > len(ms.src) || anchor {
			break
		}
	}
	L.Push(Nil)
	return 1
}

func strGmatch(L *LState) int {
	s := L.checkString(1)
	p := L.checkString(2)
	ms := newMatchState(L, s, p)
	// 5.4 init parameter: start matching from a (possibly negative) position
	// (PUC str_gmatch: init = posrelatI(opt(3,1)) - 1; a start past the end —
	// including the size_t wrap of -1 for a very negative arg — becomes len+1,
	// which yields no match because the loop stops once src > len).
	slen := int64(len(s))
	pos := L.optInt(3, 1)
	var rel int64
	switch {
	case pos > 0:
		rel = pos
	case -pos > slen:
		rel = 0
	default:
		rel = slen + pos + 1
	}
	init := rel - 1
	if init < 0 || init > slen {
		init = slen + 1
	}
	src := int(init)
	lastmatch := -1
	iter := func(L *LState) int {
		for src <= len(ms.src) {
			ms.level = 0
			ms.depth = maxMatchDepth
			e := ms.match(src, 0)
			if e != -1 && e != lastmatch {
				start := src
				src = e
				lastmatch = e
				caps := ms.pushCaptures(start, e, true)
				for _, c := range caps {
					L.Push(c)
				}
				return len(caps)
			}
			src++
		}
		return 0
	}
	// PUC str_gmatch builds the iterator as a C closure over its state, so
	// debug.upvalueid(iter, 1) is a valid id (closure.lua checks this). Mirror
	// that: expose the iterator's captured state as upvalues rather than relying
	// only on the Go closure capture above. The values are informational — the
	// iterator reads its live state through the Go capture.
	c := newGoClosure(iter, "gmatch_iter", 3)
	c.goUpvals[0] = MkString(s)
	c.goUpvals[1] = MkString(p)
	c.goUpvals[2] = Int(init)
	L.Push(mkClosure(c))
	return 1
}

func strGsub(L *LState) int {
	s := L.checkString(1)
	p := L.checkString(2)
	repl := L.Arg(3)
	maxN := L.optInt(4, int64(len(s))+1)

	ms := newMatchState(L, s, p)
	anchor := false
	pp := 0
	if len(ms.pat) > 0 && ms.pat[0] == '^' {
		anchor = true
		pp = 1
	}
	var sb strings.Builder
	count := int64(0)
	src := 0
	// lastmatch guards against an empty match immediately after the previous
	// match ended at the same spot (PUC str_gsub: e != lastmatch), so e.g.
	// gsub("a b cd", " *", "-") yields "-a-b-c-d-" rather than doubling "-".
	lastmatch := -1
	// changed tracks whether any replacement actually differed from the matched
	// text (PUC str_gsub): a table/function returning nil/false keeps the
	// original, so a no-op gsub returns the original string object unchanged.
	changed := false
	for count < maxN {
		ms.level = 0
		ms.depth = maxMatchDepth
		e := ms.match(src, pp)
		if e != -1 && e != lastmatch {
			count++
			rep, ch := gsubReplace(L, ms, src, e, repl)
			sb.WriteString(rep)
			changed = changed || ch
			src = e
			lastmatch = e
		} else if src < len(ms.src) {
			sb.WriteByte(ms.src[src])
			src++
		} else {
			break
		}
		if anchor {
			break
		}
	}
	if !changed {
		// no effective substitution: return the original string object so its
		// identity ("%p") is preserved (PUC lua_pushvalue(L, 1)).
		if a := L.Arg(1); a.IsString() {
			L.Push(a)
		} else {
			L.Push(MkString(s))
		}
		L.Push(Int(count))
		return 2
	}
	if src < len(ms.src) {
		sb.WriteString(string(ms.src[src:]))
	}
	L.Push(MkString(sb.String()))
	L.Push(Int(count))
	return 2
}

// gsubReplace computes the replacement text for one match (add_s / add_value).
// It returns the text plus whether the replacement differed from the matched
// substring (PUC add_value return value: nil/false table/function results keep
// the original text and report "no change").
func gsubReplace(L *LState, ms *matchState, s, e int, repl Value) (string, bool) {
	whole := string(ms.src[s:e])
	switch {
	case repl.IsString() || repl.IsNumber():
		r := tostr(repl)
		var sb strings.Builder
		for i := 0; i < len(r); i++ {
			if r[i] != '%' {
				sb.WriteByte(r[i])
				continue
			}
			i++
			if i >= len(r) {
				break
			}
			if r[i] == '%' {
				sb.WriteByte('%')
			} else if r[i] == '0' {
				sb.WriteString(whole)
			} else if r[i] >= '1' && r[i] <= '9' {
				sb.WriteString(tostr(ms.oneCapture(int(r[i]-'1'), s, e)))
			} else {
				L.errorf("invalid use of '%%' in replacement string")
			}
		}
		return sb.String(), true
	case repl.IsTable():
		key := ms.oneCapture(0, s, e)
		v := L.indexGet(repl, key) // PUC add_value uses lua_gettable (__index-aware)
		return gsubResult(L, v, whole)
	case repl.IsFunction():
		caps := ms.pushCaptures(s, e, true)
		res := L.callNoYield(repl, caps, 1)
		var v Value
		if len(res) > 0 {
			v = res[0]
		}
		return gsubResult(L, v, whole)
	default:
		L.argError(3, "string/function/table expected")
		return "", false
	}
}

func gsubResult(L *LState, v Value, whole string) (string, bool) {
	if v.IsFalsy() {
		return whole, false // keep original, no change
	}
	if v.IsString() || v.IsNumber() {
		return tostr(v), true
	}
	L.errorf("invalid replacement value (a %s)", typeName(v))
	return "", false
}
