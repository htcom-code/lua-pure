package luapure

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// compileSrc compiles Lua source into the main prototype via the single-pass
// front end (llex.go + lparser.go), so the luac golden validates that parser.
func compileSrc(t *testing.T, src string) *Proto {
	t.Helper()
	proto, err := compileTokens(src, "test")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	return proto
}

// luacListing compiles src with the reference `luac -l` and returns the
// instruction stream as normalized "OPNAME operands" strings, in luac's
// preorder over functions. Returns ok=false when luac is unavailable.
func luacListing(t *testing.T, src string) ([]string, bool) {
	t.Helper()
	luac, err := exec.LookPath("luac")
	if err != nil {
		return nil, false
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "in.lua")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	cmd := exec.Command(luac, "-l", path)
	cmd.Dir = dir // keep luac's default "luac.out" inside the temp dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("luac failed: %v\n%s", err, out)
	}
	var lines []string
	for _, ln := range strings.Split(string(out), "\n") {
		// Instruction lines look like: "\t<idx>\t[<line>]\tOPNAME\toperands ; comment"
		if !strings.HasPrefix(ln, "\t") {
			continue
		}
		fields := strings.Fields(ln)
		if len(fields) < 3 {
			continue
		}
		// fields[0]=idx, fields[1]=[line], fields[2]=OPNAME, rest=operands.
		if !strings.HasPrefix(fields[1], "[") {
			continue
		}
		// Drop the trailing "; comment".
		var ops []string
		for _, f := range fields[2:] {
			if f == ";" {
				break
			}
			ops = append(ops, f)
		}
		lines = append(lines, strings.Join(ops, " "))
	}
	return lines, true
}

// assertMatchesLuac compiles src both ways and asserts the instruction streams
// are identical, instruction by instruction.
func assertMatchesLuac(t *testing.T, src string) {
	t.Helper()
	want, ok := luacListing(t, src)
	if !ok {
		t.Skip("luac not available")
	}
	got := LuacListing(compileSrc(t, src))
	if len(got) != len(want) {
		t.Fatalf("instruction count: got %d, want %d\n got: %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("instr %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCodegenMatchesLuac(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"empty", ``},
		{"return constant", `return 1`},
		{"locals and arithmetic", `local x = 1; local y = x + 2; return x * y`},
		{"globals", `print(1, 2)`},
		{"string and concat", `local s = "a" .. "b" return s`},
		{"if-else", `local a = 1; if a < 10 then a = 2 else a = 3 end; return a`},
		{"if-elseif", `local a = 1
if a == 1 then return "one"
elseif a == 2 then return "two"
else return "many" end`},
		{"while loop", `local i = 0; while i < 10 do i = i + 1 end; return i`},
		{"repeat", `local i = 0; repeat i = i + 1 until i >= 5; return i`},
		{"numeric for", `local s = 0; for i = 1, 10 do s = s + i end; return s`},
		{"numeric for step", `local s = 0; for i = 10, 1, -1 do s = s + i end; return s`},
		{"generic for", `local t = {}; for k, v in pairs(t) do print(k, v) end`},
		{"function and closure", `local function f(a, b) return a + b end; return f(1, 2)`},
		{"upvalue capture", `local x = 1; local function g() return x end; return g()`},
		{"table constructor", `local t = {1, 2, 3, x = 4, [5] = 6}; return t`},
		{"method call", `local t = {}; function t:m(a) return a end; return t:m(1)`},
		{"multiple assignment", `local a, b = 1, 2; a, b = b, a; return a, b`},
		{"varargs", `local function f(...) return ... end; return f(1, 2, 3)`},
		{"and-or", `local a, b = 1, 2; return a and b or 0`},
		{"nested index", `local t = {}; t.a.b = t.x.y; return t`},
		{"bitwise", `local a, b = 6, 3; return a & b, a | b, a ~ b, a << 2, a >> 1`},
		{"length and not", `local t = {}; return #t, not t`},
		{"goto", `do goto done; ::done:: end; return 1`},
		{"comparisons", `local a, b = 1, 2; return a < b, a <= b, a == b, a ~= b, a > b, a >= b`},
		{"const attribute", `local x <const> = 42; return x`},
		{"break in loop", `for i = 1, 10 do if i == 5 then break end end`},
		{"break in while", `local i = 0; while true do i = i + 1; if i > 3 then break end end; return i`},
		{"nested closures", `local function outer() local x = 1
return function() return function() return x end end end
return outer`},
		{"recursion", `local function fact(n) if n <= 1 then return 1 end return n * fact(n - 1) end
return fact(5)`},
		{"multret into table", `local function f() return 1, 2, 3 end; local t = {f()}; return t`},
		{"multret into call", `local function f() return 1, 2 end; print(f())`},
		{"multret middle not expanded", `local function f() return 1, 2 end; print(f(), 9)`},
		{"chained concat", `local a, b, c = "x", "y", "z"; return a .. b .. c`},
		{"precedence", `return 1 + 2 * 3 - 4 / 2 ^ 2`},
		{"global assign", `x = 1; y = x; return x, y`},
		{"string method", `local s = "hi"; return s:upper()`},
		{"nested field def", `local t = {}; t.a = {}; function t.a.b() return 1 end; return t`},
		{"and-not control", `local a, b = 1, 2; if not a and b then return 1 end; return 0`},
		{"unary chain", `local a = 1; return - - a, not not a, ~ ~ a`},
		{"do block scope", `local x = 1; do local x = 2; print(x) end; return x`},
		{"deep table", `return {{1, 2}, {3, {4, 5}}, x = {y = {z = 6}}}`},
		{"many list fields", `return {1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20}`},
		{"compare chains", `local a, b, c = 1, 2, 3; return a < b and b < c`},
		{"float and int mix", `local x = 1.5; return x + 1, x * 2, 3 // 2, 3 % 2`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertMatchesLuac(t, tc.src)
		})
	}
}
