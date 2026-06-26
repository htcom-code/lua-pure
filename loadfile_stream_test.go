package luapure

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTemp writes content to a fresh temp file and returns its path.
func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestLoadfileStreamsLargeFile loads a source far larger than loadBufferSize so
// the compiler must pull many blocks across fill boundaries; a correct result
// proves the streaming reader reassembles tokens that straddle block edges.
func TestLoadfileStreamsLargeFile(t *testing.T) {
	const n = 5000 // ~ n lines of "s=s+1\n" => well over several 8 KiB blocks
	var sb strings.Builder
	sb.WriteString("local s = 0\n")
	for i := 0; i < n; i++ {
		sb.WriteString("s = s + 1\n")
	}
	sb.WriteString("return s\n")
	src := sb.String()
	if len(src) <= loadBufferSize*2 {
		t.Fatalf("source too small (%d bytes) to exercise multi-block streaming", len(src))
	}
	path := writeTemp(t, "big.lua", src)

	r := runLib(t, fmt.Sprintf(`return assert(loadfile([[%s]]))()`, path))
	wantInt(t, r[0], int64(n))
}

// TestLoadfileBinaryFile writes a string.dump blob to disk and loads it back via
// loadfile, exercising the binary-chunk branch of the streaming loader (it must
// accumulate the whole blob, then undump).
func TestLoadfileBinaryFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chunk.luac")
	r := runLib(t, fmt.Sprintf(`
		local bin = string.dump(load("return 6 * 7"))
		local f = assert(io.open([[%s]], "wb"))
		f:write(bin); f:close()
		return assert(loadfile([[%s]]))()`, path, path))
	wantInt(t, r[0], 42)
}

// TestDofileStreams checks dofile runs a streamed file and returns its results.
func TestDofileStreams(t *testing.T) {
	path := writeTemp(t, "do.lua", "return 7 + 8, 'ok'\n")
	r := runLib(t, fmt.Sprintf(`return dofile([[%s]])`, path))
	wantInt(t, r[0], 15)
	if !r[1].IsString() || r[1].Str() != "ok" {
		t.Errorf("dofile second result = %v, want \"ok\"", r[1])
	}
}

// TestLoadfileModeEnforced verifies loadfile honors the mode argument (PUC
// checkmode): "t" rejects a binary chunk, "b" rejects text.
func TestLoadfileModeEnforced(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "b.luac")
	txtPath := writeTemp(t, "t.lua", "return 1\n")
	r := runLib(t, fmt.Sprintf(`
		local bin = string.dump(load("return 1"))
		local bf = assert(io.open([[%s]], "wb")); bf:write(bin); bf:close()
		local f1, e1 = loadfile([[%s]], "t")   -- binary file, text-only mode
		local f2, e2 = loadfile([[%s]], "b")   -- text file, binary-only mode
		return f1 == nil, type(e1) == "string", f2 == nil, type(e2) == "string"`,
		binPath, binPath, txtPath))
	wantBool(t, r[0], true)
	wantBool(t, r[1], true)
	wantBool(t, r[2], true)
	wantBool(t, r[3], true)
}

// TestRequireStreamsModule loads a module file through the streaming searcher.
func TestRequireStreamsModule(t *testing.T) {
	dir := t.TempDir()
	modPath := filepath.Join(dir, "mymod.lua")
	if err := os.WriteFile(modPath, []byte("return { answer = 42 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := runLib(t, fmt.Sprintf(`
		package.path = [[%s]]
		return require("mymod").answer`, filepath.Join(dir, "?.lua")))
	wantInt(t, r[0], 42)
}
