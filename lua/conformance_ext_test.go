package luapure

import (
	"os"
	"path/filepath"
	"testing"
)

// Extended PUC-conformance regression suite: the self-asserting Lua scripts in
// _lua5.4-tests/test-ext/ (ported from the gopher-lua bugfix probes and pinned
// to PUC Lua 5.4 oracle values). Each script raises a Lua error via assert() on
// any mismatch, so a clean DoString is the pass condition. Run under -race with
// the rest of the package; keeps the original bugfixes from regressing on 5.4.
func TestConformanceExtSuite(t *testing.T) {
	files, err := filepath.Glob("../_lua5.4-tests/test-ext/*.lua")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no scripts in _lua5.4-tests/test-ext")
	}
	for _, path := range files {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			L := NewState()
			L.OpenLibs()
			// Silence each script's trailing "... all cases passed" print.
			L.SetGlobal("print", NewGoFunc("print", func(*LState) int { return 0 }))
			if _, err := L.DoString(string(src), "@"+filepath.Base(path)); err != nil {
				t.Errorf("%s: %v", filepath.Base(path), err)
			}
		})
	}
}
