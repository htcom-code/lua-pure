package luapure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bigSource concatenates the conformance fixtures into one large chunk so the
// compile benchmark exercises a realistic, string-constant-heavy program.
func bigSource(b *testing.B) string {
	b.Helper()
	files, _ := filepath.Glob("../_lua5.4-tests/*.lua")
	var sb strings.Builder
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		text := string(src)
		if strings.HasPrefix(text, "#") { // drop a leading shebang line
			if nl := strings.IndexByte(text, '\n'); nl >= 0 {
				text = text[nl+1:]
			}
		}
		// Wrap each fixture in a do-block so name clashes across files don't
		// break compilation; we only care about lexing/codegen cost, not running.
		sb.WriteString("do\n")
		sb.WriteString(text)
		sb.WriteString("\nend\n")
	}
	if sb.Len() == 0 {
		b.Skip("no fixtures")
	}
	return sb.String()
}

// BenchmarkCompile measures the compile path (lex + parse + codegen), where the
// brain note flagged MkString as a dominant cost. -benchmem shows allocs/op so
// we can see how much string-Value allocation remains after per-chunk interning.
func BenchmarkCompile(b *testing.B) {
	src := bigSource(b)
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := CompileString(src, "@bench"); err != nil {
			b.Fatalf("compile: %v", err)
		}
	}
}

// BenchmarkRuntimeStrings measures a string-construction-heavy runtime workload
// (concat / sub / format) where every result currently allocates a fresh
// luaString — the case a runtime intern table would target.
func BenchmarkRuntimeStrings(b *testing.B) {
	src := `
		local acc = 0
		for i = 1, 2000 do
			local s = "key_" .. (i % 64)      -- many repeated short strings
			local t = string.sub("abcdefghij", 1, i % 10 + 1)
			acc = acc + #s + #t
		end
		return acc`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		L := NewState()
		L.OpenLibs()
		if _, err := L.DoString(src, "=rt"); err != nil {
			b.Fatalf("run: %v", err)
		}
	}
}
