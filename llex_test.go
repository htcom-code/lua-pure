package luapure

import (
	"strings"
	"testing"
)

// tokenize runs the lexer to completion, returning a compact description of each
// token (kind plus payload) for assertions.
func tokenize(t *testing.T, src string) []string {
	t.Helper()
	ls := newLexState(src, "=test")
	var out []string
	for {
		ls.luaXNext()
		tk := ls.t
		if tk.kind == tkEOS {
			break
		}
		switch tk.kind {
		case tkName:
			out = append(out, "name:"+tk.str)
		case tkString:
			out = append(out, "str:"+tk.str)
		case tkInt:
			out = append(out, "int:"+numToString(tk.num))
		case tkFlt:
			out = append(out, "flt:"+numToString(tk.num))
		default:
			out = append(out, token2str(tk.kind))
		}
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLexTokens(t *testing.T) {
	cases := []struct {
		src  string
		want []string
	}{
		{`local x = 1 + 2.5`, []string{"'local'", "name:x", "'='", "int:1", "'+'", "flt:2.5"}},
		{`a.b:c()`, []string{"name:a", "'.'", "name:b", "':'", "name:c", "'('", "')'"}},
		{`x // 2 ~ 3 << 4 >> 5`, []string{"name:x", "'//'", "int:2", "'~'", "int:3", "'<<'", "int:4", "'>>'", "int:5"}},
		{`a == b ~= c <= d >= e .. f ... ::g::`, []string{
			"name:a", "'=='", "name:b", "'~='", "name:c", "'<='", "name:d", "'>='", "name:e",
			"'..'", "name:f", "'...'", "'::'", "name:g", "'::'"}},
		{`0xff 0x1p4 1e3 .5 3.`, []string{"int:255", "flt:16.0", "flt:1000.0", "flt:0.5", "flt:3.0"}},
		{`"a\tb\65\x42\u{43}" 'q'`, []string{"str:a\tbABC", "str:q"}},
		{"[[long\nstring]]", []string{"str:long\nstring"}},
		{"[==[ a]]b ]==]", []string{"str: a]]b "}},
		{"x --[[block\ncomment]] y", []string{"name:x", "name:y"}},
		{"x -- line comment\ny", []string{"name:x", "name:y"}},
		{`and or not nil true false function end`, []string{
			"'and'", "'or'", "'not'", "'nil'", "'true'", "'false'", "'function'", "'end'"}},
		{`{[1]=2}`, []string{"'{'", "'['", "int:1", "']'", "'='", "int:2", "'}'"}},
	}
	for _, c := range cases {
		got := tokenize(t, c.src)
		if !eq(got, c.want) {
			t.Errorf("%q\n got=%v\nwant=%v", c.src, got, c.want)
		}
	}
}

func TestLexLongStringLeadingNewline(t *testing.T) {
	// A long string opened immediately before a newline drops that newline.
	got := tokenize(t, "[[\nabc]]")
	if len(got) != 1 || got[0] != "str:abc" {
		t.Errorf("got=%v want=[str:abc]", got)
	}
}

func TestLexLineCount(t *testing.T) {
	ls := newLexState("a\nb\n\nc", "=test")
	ls.luaXNext() // a (line 1)
	ls.luaXNext() // b
	if ls.line != 2 {
		t.Errorf("after b: line=%d want 2", ls.line)
	}
	ls.luaXNext() // c (line 4)
	if ls.t.str != "c" || ls.line != 4 {
		t.Errorf("c: str=%q line=%d want c/4", ls.t.str, ls.line)
	}
}

func lexErrMsg(src string) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			if ce, ok := r.(*CompileError); ok {
				msg = ce.Msg
			} else {
				msg = "panic"
			}
		}
	}()
	ls := newLexState(src, "=test")
	for {
		ls.luaXNext()
		if ls.t.kind == tkEOS {
			return "no error"
		}
	}
}

func TestLexErrors(t *testing.T) {
	cases := []struct{ src, want string }{
		{`"unterminated`, "test:1: unfinished string near <eof>"},
		{"\"line\nbreak\"", "test:1: unfinished string"},
		{`0x`, "test:1: malformed number near '0x'"},
		{`1..2`, "test:1: malformed number near '1..2'"},
		{`"\x"`, "test:1: hexadecimal digit expected"},
		{`"\999"`, "test:1: decimal escape too large"},
		{`[==[ unfinished`, "test:1: unfinished long string (starting at line 1) near <eof>"},
	}
	for _, c := range cases {
		got := lexErrMsg(c.src)
		if !strings.Contains(got, c.want) {
			t.Errorf("%q\n got=%q\nwant(contains)=%q", c.src, got, c.want)
		}
	}
}
