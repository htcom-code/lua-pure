package luapure_test

import (
	"testing"

	luapure "github.com/htcom-code/lua-pure"
)

// A line hook fires once per source line and can read the stopped frame's
// position and locals.
func TestGoHookLineAndFrame(t *testing.T) {
	L := luapure.NewState()
	L.OpenLibs()

	type stop struct {
		line  int
		x     int64
		xName string
	}
	var stops []stop

	L.SetGoHook(func(L *luapure.LState, ev luapure.HookEvent, line int) {
		if ev != luapure.HookLine {
			return
		}
		f, ok := L.Frame(0)
		if !ok {
			t.Error("Frame(0) not available in a line hook")
			return
		}
		s := stop{line: line}
		if f.CurrentLine() != line {
			t.Errorf("CurrentLine %d != hook line %d", f.CurrentLine(), line)
		}
		// Read local "x" when present.
		for _, lv := range f.Locals() {
			if lv.Name == "x" {
				s.xName = lv.Name
				s.x = lv.Value.AsInt()
			}
		}
		stops = append(stops, s)
	}, luapure.MaskLine, 0)

	_, err := L.DoString("local x = 1\nx = x + 10\nx = x + 100\n", "=t")
	if err != nil {
		t.Fatal(err)
	}
	if len(stops) == 0 {
		t.Fatal("line hook never fired")
	}
	// The final stop should observe x already assigned on an earlier line.
	last := stops[len(stops)-1]
	if last.xName != "x" {
		t.Fatalf("expected to observe local x, last stop = %+v", last)
	}
}

// ClearGoHook stops further firing.
func TestClearGoHook(t *testing.T) {
	L := luapure.NewState()
	L.OpenLibs()

	fires := 0
	L.SetGoHook(func(L *luapure.LState, ev luapure.HookEvent, line int) { fires++ }, luapure.MaskLine, 0)
	if _, err := L.DoString("local a=1\na=2\n", "=t"); err != nil {
		t.Fatal(err)
	}
	got := fires
	if got == 0 {
		t.Fatal("hook should have fired")
	}
	L.ClearGoHook()
	if _, err := L.DoString("local a=1\na=2\na=3\n", "=t"); err != nil {
		t.Fatal(err)
	}
	if fires != got {
		t.Fatalf("hook fired %d more times after ClearGoHook", fires-got)
	}
}

// Call and return events fire on function entry/exit, letting a hook track call
// depth (the basis for step-over/step-out).
func TestGoHookCallReturnDepth(t *testing.T) {
	L := luapure.NewState()
	L.OpenLibs()

	depth, maxDepth := 0, 0
	L.SetGoHook(func(L *luapure.LState, ev luapure.HookEvent, line int) {
		switch ev {
		case luapure.HookCall, luapure.HookTailCall:
			depth++
			if depth > maxDepth {
				maxDepth = depth
			}
		case luapure.HookReturn:
			depth--
		}
	}, luapure.MaskCall|luapure.MaskReturn, 0)

	_, err := L.DoString(`
		local function inner() return 1 end
		local function outer() return inner() + inner() end
		return outer()`, "=t")
	if err != nil {
		t.Fatal(err)
	}
	// main calls outer calls inner: nesting reaches at least depth 2 below main.
	if maxDepth < 2 {
		t.Fatalf("max observed call depth = %d, want >= 2", maxDepth)
	}
}

// SetLocal / SetUpvalue from a hook mutate the running program's state.
func TestGoHookSetLocal(t *testing.T) {
	L := luapure.NewState()
	L.OpenLibs()

	patched := false
	L.SetGoHook(func(L *luapure.LState, ev luapure.HookEvent, line int) {
		if ev != luapure.HookLine || patched {
			return
		}
		f, ok := L.Frame(0)
		if !ok {
			return
		}
		for _, lv := range f.Locals() {
			if lv.Name == "n" && lv.Value.AsInt() == 1 {
				f.SetLocal(lv.Index, luapure.Int(41))
				patched = true
			}
		}
	}, luapure.MaskLine, 0)

	res, err := L.DoString("local n = 1\nreturn n + 1\n", "=t")
	if err != nil {
		t.Fatal(err)
	}
	if !patched {
		t.Fatal("hook never patched local n")
	}
	if got := res[0].AsInt(); got != 42 {
		t.Fatalf("result = %d, want 42 (local patched to 41)", got)
	}
}

// StackDepth and Frame walk callers; FuncName names them from the call site.
func TestFrameWalk(t *testing.T) {
	L := luapure.NewState()
	L.OpenLibs()

	var names []string
	var depth int
	captured := false
	L.SetGoHook(func(L *luapure.LState, ev luapure.HookEvent, line int) {
		if ev != luapure.HookCall || captured {
			return
		}
		f, _ := L.Frame(0)
		name, _ := f.FuncName()
		if name == "target" {
			captured = true
			depth = L.StackDepth()
			for lvl := 0; lvl < depth; lvl++ {
				fr, ok := L.Frame(lvl)
				if !ok {
					break
				}
				n, _ := fr.FuncName()
				names = append(names, n)
			}
		}
	}, luapure.MaskCall, 0)

	// Non-tail calls so the frames genuinely stack (a tail call would replace
	// the caller's frame).
	_, err := L.DoString(`
		local function target() return 1 end
		local function mid() local x = target(); return x end
		local y = mid(); return y`, "=t")
	if err != nil {
		t.Fatal(err)
	}
	if !captured {
		t.Fatal("never entered target")
	}
	if depth < 3 {
		t.Fatalf("stack depth at target = %d, want >= 3 (target, mid, main)", depth)
	}
	if names[0] != "target" {
		t.Fatalf("frame 0 name = %q, want \"target\"", names[0])
	}
}

// A Go hook and a Lua debug.sethook hook coexist, both firing.
func TestGoHookCoexistsWithLuaHook(t *testing.T) {
	L := luapure.NewState()
	L.OpenLibs()

	goFires := 0
	L.SetGoHook(func(L *luapure.LState, ev luapure.HookEvent, line int) {
		if ev == luapure.HookLine {
			goFires++
		}
	}, luapure.MaskLine, 0)

	_, err := L.DoString(`
		local n = 0
		debug.sethook(function() n = n + 1 end, "l")
		local a = 1
		a = a + 1
		luafires = n`, "=t")
	if err != nil {
		t.Fatal(err)
	}
	if goFires == 0 {
		t.Fatal("Go line hook did not fire")
	}
	if L.GetGlobal("luafires").AsInt() == 0 {
		t.Fatal("Lua line hook did not fire alongside the Go hook")
	}
}
