// conformance runs the official Lua 5.4 test suite against the luapure engine
// and reports per-file status — the project's conformance oracle.
//
//	go run ./cmd/conformance                # run, print table + summary
//	go run ./cmd/conformance -dir DIR        # custom suite dir
//	go run ./cmd/conformance -timeout 10s    # per-file timeout
//	go run ./cmd/conformance -v              # show error detail per file
//
// Each file runs in its own child process so a non-terminating test is killed
// cleanly without starving the rest of the suite.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	luapure "github.com/htcom-code/lua-pure/lua"
)

// driverFiles need the full harness / internal C test library `T` / os.exit and
// are not meaningful to run standalone. Skipped (reported as SKIP).
var driverFiles = map[string]bool{
	"all.lua":  true, // top-level driver: requires every other file + os.exit
	"main.lua": true, // spawns the lua binary as a subprocess
}

type result struct {
	file   string
	status string // PASS | LOAD-ERR | RUN-ERR | PANIC | TIMEOUT | SKIP
	detail string
}

// runOne runs a single test file in this process (no internal timeout — the
// parent kills the process on deadline).
func runOne(dir, file string) (status, detail string) {
	src, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return "LOAD-ERR", err.Error()
	}
	defer func() {
		if r := recover(); r != nil {
			status, detail = "PANIC", fmt.Sprintf("%v", r)
		}
	}()
	L := luapure.NewState()
	L.OpenLibs()
	L.SetGlobal("print", luapure.NewGoFunc("print", func(*luapure.LState) int { return 0 }))
	// _port marks a non-reference/portable platform: PUC's own suite uses it to
	// skip OS/stdio/process-specific blocks (a standalone interpreter re-exec via
	// arg + io.popen/os.execute, time_t extremes, etc.). luapure is an embedded
	// pure-Go VM that intentionally does not spawn processes, so those blocks are
	// structurally inapplicable, not engine bugs — set _port to skip them, exactly
	// as PUC does on non-Unix hosts.
	L.SetGlobal("_port", luapure.True)
	// big.lua yields at the top level; PUC all.lua runs it inside a coroutine
	// and resumes until it finishes. Drive it the same way.
	if file == "big.lua" {
		L.SetGlobal("__CONF_SRC", luapure.MkString(string(src)))
		L.SetGlobal("__CONF_NAME", luapure.MkString("@"+file))
		driver := `local chunk = assert(load(__CONF_SRC, __CONF_NAME))
local co = coroutine.create(chunk)
while true do
  local ok, v = coroutine.resume(co)
  if not ok then error(v, 0) end
  if coroutine.status(co) == "dead" then break end
end`
		if _, err := L.DoString(driver, "@conf_driver"); err != nil {
			return "RUN-ERR", firstLine(err.Error())
		}
		return "PASS", ""
	}
	if _, err := L.DoString(string(src), "@"+file); err != nil {
		if strings.Contains(err.Error(), "parse") || strings.Contains(err.Error(), "syntax") {
			return "LOAD-ERR", firstLine(err.Error())
		}
		return "RUN-ERR", firstLine(err.Error())
	}
	return "PASS", ""
}

// runIsolated runs one test file in a child process (self -file F) under a
// context timeout, so a non-terminating test reports TIMEOUT without stealing
// CPU/memory from later tests (Go cannot kill a goroutine).
func runIsolated(self, dir, file string, timeout time.Duration) (status, detail string) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, "-dir", dir, "-file", file)
	cmd.Dir = dir
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return "TIMEOUT", ""
	}
	if err != nil {
		return "PANIC", firstLine(err.Error())
	}
	s := string(out)
	// The worker wraps its result in resultMarker; everything before it is the
	// test's own stdout and is ignored.
	idx := strings.LastIndex(s, resultMarker)
	if idx < 0 {
		return "PANIC", "no result marker in output: " + firstLine(s)
	}
	res := s[idx+len(resultMarker):]
	if i := strings.IndexByte(res, '\t'); i >= 0 {
		return res[:i], res[i+1:]
	}
	return res, ""
}

// resultMarker delimits the worker's STATUS\tDETAIL line from any stdout the
// test itself produced.
const resultMarker = "@@luapure-conf-result@@"

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 100 {
		s = s[:100] + "…"
	}
	return s
}

func main() {
	dir := flag.String("dir", "_lua5.4-tests", "directory holding the Lua 5.4 test suite")
	timeout := flag.Duration("timeout", 15*time.Second, "per-file timeout")
	verbose := flag.Bool("v", false, "print error detail per file")
	file := flag.String("file", "", "internal: run a single file in this process and print STATUS\\tDETAIL")
	flag.Parse()

	// Cap runaway growth so OOM-stress tests raise catchable errors instead of
	// crashing the process (Go has no recoverable allocation-failure path).
	luapure.MaxTableArraySize = 1 << 22 // ~4.2M entries
	luapure.MaxLexElement = 1 << 26     // 64 MB single token

	abs, err := filepath.Abs(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Single-file worker mode: run one file directly and print the result.
	// The result is wrapped in resultMarker so a test that writes to stdout
	// (e.g. files.lua's "test done" line) cannot be mistaken for the status.
	if *file != "" {
		os.Chdir(abs)
		st, dt := runOne(abs, *file)
		fmt.Printf("\n%s%s\t%s", resultMarker, st, dt)
		return
	}

	entries, err := filepath.Glob(filepath.Join(abs, "*.lua"))
	if err != nil || len(entries) == 0 {
		fmt.Fprintf(os.Stderr, "no .lua files in %s\n", abs)
		os.Exit(1)
	}
	sort.Strings(entries)

	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}

	var results []result
	counts := map[string]int{}
	for _, p := range entries {
		f := filepath.Base(p)
		if driverFiles[f] {
			results = append(results, result{f, "SKIP", "driver/needs C lib T"})
			counts["SKIP"]++
			continue
		}
		st, dt := runIsolated(self, abs, f, *timeout)
		results = append(results, result{f, st, dt})
		counts[st]++
	}

	fmt.Println("Lua 5.4 conformance — luapure")
	fmt.Println(strings.Repeat("-", 72))
	for _, r := range results {
		line := fmt.Sprintf("  %-9s %s", r.status, r.file)
		if *verbose && r.detail != "" {
			line += "  — " + r.detail
		}
		fmt.Println(line)
	}
	fmt.Println(strings.Repeat("-", 72))
	fmt.Printf("PASS %d/%d   (RUN-ERR %d, LOAD-ERR %d, PANIC %d, TIMEOUT %d, SKIP %d)\n",
		counts["PASS"], len(results), counts["RUN-ERR"], counts["LOAD-ERR"],
		counts["PANIC"], counts["TIMEOUT"], counts["SKIP"])
}
