package luapure

import (
	"fmt"
	"math"
)

// Lexical analyzer — a faithful port of PUC llex.c / llex.h. It turns Lua source
// into a token stream with one-token lookahead, the front end of the single-pass
// recursive-descent compiler (replacing the AST-based path for the luapure engine).

const eoz = -1 // end of input (PUC EOZ)

// firstReserved is PUC FIRST_RESERVED (UCHAR_MAX + 1): single-byte tokens use
// their own byte value, reserved words and multi-char symbols start here.
const firstReserved = 257

// Token kinds (ORDER RESERVED — must match luaXTokens below and PUC's enum).
const (
	tkAnd = firstReserved + iota
	tkBreak
	tkDo
	tkElse
	tkElseif
	tkEnd
	tkFalse
	tkFor
	tkFunction
	tkGoto
	tkIf
	tkIn
	tkLocal
	tkNil
	tkNot
	tkOr
	tkRepeat
	tkReturn
	tkThen
	tkTrue
	tkUntil
	tkWhile
	// other terminal symbols
	tkIDiv
	tkConcat
	tkDots
	tkEq
	tkGe
	tkLe
	tkNe
	tkShl
	tkShr
	tkDbColon
	tkEOS
	tkFlt
	tkInt
	tkName
	tkString
)

const numReserved = tkWhile - firstReserved + 1

// luaXTokens mirrors PUC's table; index = token - firstReserved.
var luaXTokens = []string{
	"and", "break", "do", "else", "elseif",
	"end", "false", "for", "function", "goto", "if",
	"in", "local", "nil", "not", "or", "repeat",
	"return", "then", "true", "until", "while",
	"//", "..", "...", "==", ">=", "<=", "~=",
	"<<", ">>", "::", "<eof>",
	"<number>", "<integer>", "<name>", "<string>",
}

// reservedWords maps a word to its token kind (only the first numReserved
// entries of luaXTokens are reserved words).
var reservedWords = func() map[string]int {
	m := make(map[string]int, numReserved)
	for i := 0; i < numReserved; i++ {
		m[luaXTokens[i]] = firstReserved + i
	}
	return m
}()

// token carries a kind plus the semantic payload for literals.
type token struct {
	kind int
	num  Value  // tkInt / tkFlt
	str  string // tkName / tkString
}

// lexState is PUC LexState (scanner half; the parser fields live on compiler).
type lexState struct {
	z         *ZIO // input byte stream (PUC LexState.z)
	current   int  // current character, or eoz
	line      int  // linenumber
	lastline  int  // line of the last consumed token
	t         token
	lookahead token
	buff      []byte // token buffer
	source    string // chunk name (for messages)
}

// newLexState builds a lexer over a complete string (PUC luaX_setinput over a
// string ZIO). Kept for the common path and tests.
func newLexState(src, source string) *lexState {
	return newLexStateZIO(newStringZIO(src), source)
}

// newLexStateZIO builds a lexer that pulls bytes from z (PUC luaX_setinput).
func newLexStateZIO(z *ZIO, source string) *lexState {
	ls := &lexState{z: z, source: source, line: 1, lastline: 1}
	ls.lookahead.kind = tkEOS // no look-ahead yet
	ls.next()                 // prime ls.current
	return ls
}

// --- character helpers (lctype.h, ASCII / C locale) ---

func lisdigit(c int) bool  { return c >= '0' && c <= '9' }
func lisxdigit(c int) bool { return lisdigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') }
func lisalpha(c int) bool  { return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func lisalnum(c int) bool  { return lisalpha(c) || lisdigit(c) }
func lisspace(c int) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r'
}
func lisprint(c int) bool { return c >= 0x20 && c < 0x7f }

func hexavalue(c int) int {
	switch {
	case lisdigit(c):
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

// --- low-level scanning ---

func (ls *lexState) next() { ls.current = ls.z.getc() }

// MaxLexElement (luaconf.go) caps the length of a single lexical element.

func (ls *lexState) save(c int) {
	if MaxLexElement > 0 && len(ls.buff) >= MaxLexElement {
		ls.lexError("lexical element too long", 0)
	}
	ls.buff = append(ls.buff, byte(c))
}

func (ls *lexState) saveAndNext() {
	ls.save(ls.current)
	ls.next()
}

func (ls *lexState) currIsNewline() bool { return ls.current == '\n' || ls.current == '\r' }

func (ls *lexState) resetBuffer() { ls.buff = ls.buff[:0] }

func (ls *lexState) buffRemove(n int) { ls.buff = ls.buff[:len(ls.buff)-n] }

// --- errors (lexerror / luaX_syntaxerror) ---

func (ls *lexState) lexError(msg string, tok int) {
	full := fmt.Sprintf("%s:%d: %s", shortSrc(ls.source), ls.line, msg)
	if tok != 0 {
		full += " near " + ls.txtToken(tok)
	}
	panic(&CompileError{Msg: full, Line: ls.line})
}

func (ls *lexState) syntaxError(msg string) { ls.lexError(msg, ls.t.kind) }

func (ls *lexState) txtToken(tok int) string {
	switch tok {
	case tkName, tkString, tkFlt, tkInt:
		return "'" + string(ls.buff) + "'"
	default:
		return token2str(tok)
	}
}

// token2str renders a token for messages (luaX_token2str).
func token2str(tok int) string {
	if tok < firstReserved { // single-byte symbol
		if lisprint(tok) {
			return fmt.Sprintf("'%c'", tok)
		}
		return fmt.Sprintf("'<\\%d>'", tok)
	}
	s := luaXTokens[tok-firstReserved]
	if tok < tkEOS { // fixed format (symbols and reserved words)
		return "'" + s + "'"
	}
	return s // names, strings, numerals
}

// inclinenumber increments the line counter, skipping a \n, \r, \n\r or \r\n
// sequence (PUC inclinenumber).
func (ls *lexState) inclinenumber() {
	old := ls.current
	ls.next() // skip '\n' or '\r'
	if ls.currIsNewline() && ls.current != old {
		ls.next() // skip '\n\r' or '\r\n'
	}
	ls.line++
	// PUC inclinenumber caps the line counter at MAX_INT; our line is a wider
	// Go int, so enforce the same 32-bit ceiling to report "chunk has too many
	// lines" deterministically (heavy.lua manylines) instead of counting forever.
	if ls.line >= math.MaxInt32 {
		ls.lexError("chunk has too many lines", 0)
	}
}

func (ls *lexState) checkNext1(c int) bool {
	if ls.current == c {
		ls.next()
		return true
	}
	return false
}

// checkNext2 accepts and saves the current char if it is one of set's two chars.
func (ls *lexState) checkNext2(set string) bool {
	if ls.current == int(set[0]) || ls.current == int(set[1]) {
		ls.saveAndNext()
		return true
	}
	return false
}

// readNumeral reads a numeral (PUC read_numeral): a liberal prefix is collected
// then validated/converted by parseNumeral.
func (ls *lexState) readNumeral(t *token) int {
	expo := "Ee"
	first := ls.current
	ls.saveAndNext()
	if first == '0' && ls.checkNext2("xX") { // hexadecimal?
		expo = "Pp"
	}
	for {
		if ls.checkNext2(expo) { // exponent mark?
			ls.checkNext2("-+") // optional sign
		} else if lisxdigit(ls.current) || ls.current == '.' {
			ls.saveAndNext()
		} else {
			break
		}
	}
	if lisalpha(ls.current) { // numeral touching a letter? force an error
		ls.saveAndNext()
	}
	v, ok := parseNumeral(string(ls.buff))
	if !ok {
		ls.lexError("malformed number", tkFlt)
	}
	t.num = v
	if v.IsInt() {
		return tkInt
	}
	return tkFlt
}

// skipSep reads a run '[=*[' or ']=*]', leaving the last bracket. Returns the
// number of '='s + 2 if well formed, 1 for a single bracket, else 0.
func (ls *lexState) skipSep() int {
	count := 0
	s := ls.current
	ls.saveAndNext()
	for ls.current == '=' {
		ls.saveAndNext()
		count++
	}
	switch {
	case ls.current == s:
		return count + 2
	case count == 0:
		return 1
	default:
		return 0
	}
}

// readLongString reads a long string/comment body (PUC read_long_string). When
// keep is false it is a comment (the body is discarded).
func (ls *lexState) readLongString(t *token, sep int, keep bool) {
	line := ls.line
	ls.saveAndNext() // skip 2nd '['
	if ls.currIsNewline() {
		ls.inclinenumber() // skip a leading newline
	}
	for {
		switch ls.current {
		case eoz:
			what := "comment"
			if keep {
				what = "string"
			}
			ls.lexError(fmt.Sprintf("unfinished long %s (starting at line %d)", what, line), tkEOS)
		case ']':
			if ls.skipSep() == sep {
				ls.saveAndNext() // skip 2nd ']'
				goto endloop
			}
		case '\n', '\r':
			ls.save('\n')
			ls.inclinenumber()
			if !keep {
				ls.resetBuffer()
			}
		default:
			if keep {
				ls.saveAndNext()
			} else {
				ls.next()
			}
		}
	}
endloop:
	if keep {
		t.str = string(ls.buff[sep : len(ls.buff)-sep])
	}
}

func (ls *lexState) esccheck(ok bool, msg string) {
	if !ok {
		if ls.current != eoz {
			ls.saveAndNext()
		}
		ls.lexError(msg, tkString)
	}
}

func (ls *lexState) gethexa() int {
	ls.saveAndNext()
	ls.esccheck(lisxdigit(ls.current), "hexadecimal digit expected")
	return hexavalue(ls.current)
}

func (ls *lexState) readhexaesc() int {
	r := ls.gethexa()
	r = (r << 4) + ls.gethexa()
	ls.buffRemove(2)
	return r
}

func (ls *lexState) readutf8esc() uint32 {
	i := 4 // chars to remove: '\', 'u', '{', first digit
	ls.saveAndNext()
	ls.esccheck(ls.current == '{', "missing '{'")
	r := uint32(ls.gethexa())
	for {
		ls.saveAndNext()
		if !lisxdigit(ls.current) {
			break
		}
		i++
		ls.esccheck(r <= (0x7FFFFFFF>>4), "UTF-8 value too large")
		r = (r << 4) + uint32(hexavalue(ls.current))
	}
	ls.esccheck(ls.current == '}', "missing '}'")
	ls.next() // skip '}'
	ls.buffRemove(i)
	return r
}

func (ls *lexState) utf8esc() {
	for _, b := range utf8esc(ls.readutf8esc()) {
		ls.save(int(b))
	}
}

func (ls *lexState) readdecesc() int {
	r := 0
	i := 0
	for ; i < 3 && lisdigit(ls.current); i++ {
		r = 10*r + (ls.current - '0')
		ls.saveAndNext()
	}
	ls.esccheck(r <= 255, "decimal escape too large")
	ls.buffRemove(i)
	return r
}

// escOnlySave is PUC's only_save: drop the saved '\\' and save the final char.
func (ls *lexState) escOnlySave(c int) {
	ls.buffRemove(1)
	ls.save(c)
}

// escReadSave is PUC's read_save: consume the escape's last char, then only_save.
func (ls *lexState) escReadSave(c int) {
	ls.next()
	ls.escOnlySave(c)
}

func (ls *lexState) readString(del int, t *token) {
	ls.saveAndNext() // keep delimiter (for error messages)
	for ls.current != del {
		switch ls.current {
		case eoz:
			ls.lexError("unfinished string", tkEOS)
		case '\n', '\r':
			ls.lexError("unfinished string", tkString)
		case '\\': // escape sequences
			ls.saveAndNext() // keep '\\' for error messages
			// PUC goto chain: read_save = next()+only_save; only_save =
			// buffRemove('\\')+save(c); no_save = nothing.
			switch ls.current {
			case 'a':
				ls.escReadSave('\a')
			case 'b':
				ls.escReadSave('\b')
			case 'f':
				ls.escReadSave('\f')
			case 'n':
				ls.escReadSave('\n')
			case 'r':
				ls.escReadSave('\r')
			case 't':
				ls.escReadSave('\t')
			case 'v':
				ls.escReadSave('\v')
			case 'x':
				ls.escReadSave(ls.readhexaesc())
			case 'u':
				ls.utf8esc() // no_save: appended directly
			case '\n', '\r':
				ls.inclinenumber()
				ls.escOnlySave('\n')
			case '\\', '"', '\'':
				ls.escReadSave(ls.current)
			case eoz:
				// no_save: will raise "unfinished string" next loop
			case 'z': // zap following span of spaces
				ls.buffRemove(1) // remove '\\'
				ls.next()        // skip 'z'
				for lisspace(ls.current) {
					if ls.currIsNewline() {
						ls.inclinenumber()
					} else {
						ls.next()
					}
				}
			default:
				ls.esccheck(lisdigit(ls.current), "invalid escape sequence")
				ls.escOnlySave(ls.readdecesc()) // '\ddd'
			}
		default:
			ls.saveAndNext()
		}
	}
	ls.saveAndNext() // skip delimiter
	t.str = string(ls.buff[1 : len(ls.buff)-1])
}

// llex scans one token, filling t's payload and returning its kind (PUC llex).
func (ls *lexState) llex(t *token) int {
	ls.resetBuffer()
	for {
		switch ls.current {
		case '\n', '\r':
			ls.inclinenumber()
		case ' ', '\f', '\t', '\v':
			ls.next()
		case '-':
			ls.next()
			if ls.current != '-' {
				return '-'
			}
			ls.next() // second '-'
			if ls.current == '[' {
				sep := ls.skipSep()
				ls.resetBuffer()
				if sep >= 2 {
					ls.readLongString(t, sep, false)
					ls.resetBuffer()
					continue
				}
			}
			for !ls.currIsNewline() && ls.current != eoz {
				ls.next() // short comment to end of line
			}
		case '[':
			sep := ls.skipSep()
			if sep >= 2 {
				ls.readLongString(t, sep, true)
				return tkString
			} else if sep == 0 {
				ls.lexError("invalid long string delimiter", tkString)
			}
			return '['
		case '=':
			ls.next()
			if ls.checkNext1('=') {
				return tkEq
			}
			return '='
		case '<':
			ls.next()
			if ls.checkNext1('=') {
				return tkLe
			} else if ls.checkNext1('<') {
				return tkShl
			}
			return '<'
		case '>':
			ls.next()
			if ls.checkNext1('=') {
				return tkGe
			} else if ls.checkNext1('>') {
				return tkShr
			}
			return '>'
		case '/':
			ls.next()
			if ls.checkNext1('/') {
				return tkIDiv
			}
			return '/'
		case '~':
			ls.next()
			if ls.checkNext1('=') {
				return tkNe
			}
			return '~'
		case ':':
			ls.next()
			if ls.checkNext1(':') {
				return tkDbColon
			}
			return ':'
		case '"', '\'':
			ls.readString(ls.current, t)
			return tkString
		case '.':
			ls.saveAndNext()
			if ls.checkNext1('.') {
				if ls.checkNext1('.') {
					return tkDots
				}
				return tkConcat
			} else if !lisdigit(ls.current) {
				return '.'
			}
			return ls.readNumeral(t)
		case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			return ls.readNumeral(t)
		case eoz:
			return tkEOS
		default:
			if lisalpha(ls.current) { // identifier or reserved word
				for {
					ls.saveAndNext()
					if !lisalnum(ls.current) {
						break
					}
				}
				word := string(ls.buff)
				t.str = word
				if kind, ok := reservedWords[word]; ok {
					return kind
				}
				return tkName
			}
			// single-char token ('+', '*', '%', '{', '}', ...)
			c := ls.current
			ls.next()
			return c
		}
	}
}

// luaXNext advances to the next token (using the look-ahead if buffered).
func (ls *lexState) luaXNext() {
	ls.lastline = ls.line
	if ls.lookahead.kind != tkEOS { // a look-ahead token is available
		ls.t = ls.lookahead
		ls.lookahead.kind = tkEOS
	} else {
		ls.t.kind = ls.llex(&ls.t)
	}
}

// luaXLookahead reads (and buffers) the token after the current one.
func (ls *lexState) luaXLookahead() int {
	ls.lookahead.kind = ls.llex(&ls.lookahead)
	return ls.lookahead.kind
}
