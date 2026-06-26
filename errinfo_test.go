package luapure

import (
	"strings"
	"testing"
)

// errMsg runs src expecting a runtime error and returns its message.
func errMsg(t *testing.T, src string) string {
	t.Helper()
	L := NewState()
	L.OpenLibs()
	_, err := L.DoString(src, "=t")
	if err == nil {
		t.Fatalf("expected error from %q", src)
	}
	return err.Error()
}

func TestErrorNameInfo(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`bbbb(3)`, "(global 'bbbb')"},
		{`local t = {}; t:bbbb(3)`, "(method 'bbbb')"},
		{`local t = {}; return t.x.y`, "(field 'x')"},
		{`return nope.x`, "(global 'nope')"},
		{`local f = 5; f()`, "(local 'f')"},
		{`local up = nil; local function g() return up.x end; return g()`, "(upvalue 'up')"},
		{`local t = {}; t.field = nil; return t.field.deep`, "(field 'field')"},
	}
	for _, c := range cases {
		got := errMsg(t, c.src)
		if !strings.Contains(got, c.want) {
			t.Errorf("%q: got %q, want substring %q", c.src, got, c.want)
		}
	}
}

func TestErrorMessageSubstrings(t *testing.T) {
	// Substrings the conformance suite's checkmessage/checkerror helpers search.
	cases := []struct {
		src  string
		want string
	}{
		{`return {} + 1`, "arithmetic"},
		{`return {} | 1`, "bitwise operation"},
		{`return {} < 1`, "attempt to compare"},
		{`return {} <= 1`, "attempt to compare"},
		{`return 1 < "x"`, "attempt to compare"},
		{`return #nil`, "length of"},
		{`return nil .. "x"`, "concatenate"},
		{`return 1 // 0`, "divide by zero"},
		{`return 1 % 0`, "'n%0'"},
	}
	for _, c := range cases {
		got := errMsg(t, c.src)
		if !strings.Contains(got, c.want) {
			t.Errorf("%q: got %q, want substring %q", c.src, got, c.want)
		}
	}
}
