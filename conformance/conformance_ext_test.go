package conformance

import (
	"os"
	"path/filepath"
	"testing"

	luapure "github.com/htcom-code/lua-pure/lua"
)

// Extended PUC-conformance regression suite: the self-asserting Lua scripts in
// _glue5.4-tests/, pinned to PUC Lua 5.4 oracle values, covering behaviours
// that are easy to get subtly wrong (error messages, number formatting,
// coercions, GC counts, …). Each script raises a Lua error via assert() on any
// mismatch, so a clean DoString is the pass condition. Run under -race with the
// rest of the package to keep these behaviours from regressing.
func TestConformanceExtSuite(t *testing.T) {
	files, err := filepath.Glob("../_glue5.4-tests/*.lua")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no scripts in _glue5.4-tests")
	}
	for _, path := range files {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			L := luapure.NewState()
			L.OpenLibs()
			// Silence each script's trailing "... all cases passed" print.
			L.SetGlobal("print", luapure.NewGoFunc("print", func(*luapure.LState) int { return 0 }))
			if _, err := L.DoString(string(src), "@"+filepath.Base(path)); err != nil {
				t.Errorf("%s: %v", filepath.Base(path), err)
			}
		})
	}
}
