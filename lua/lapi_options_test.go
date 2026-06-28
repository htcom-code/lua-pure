package luapure

import (
	"strings"
	"testing"
)

// NewState with no options matches the package globals; options override this
// state's own copy without touching the globals or other states.
func TestNewStateOptionsPerStateConfig(t *testing.T) {
	def := NewState()
	if def.cfg.maxStack != MaxStack || def.cfg.maxCCalls != MaxCCalls || def.cfg.maxTableArraySize != MaxTableArraySize {
		t.Fatalf("default cfg %+v does not mirror globals (MaxStack=%d MaxCCalls=%d MaxTableArraySize=%d)",
			def.cfg, MaxStack, MaxCCalls, MaxTableArraySize)
	}

	custom := NewState(WithMaxStack(123), WithMaxCCalls(7), WithMaxTableArraySize(9))
	if custom.cfg.maxStack != 123 || custom.cfg.maxCCalls != 7 || custom.cfg.maxTableArraySize != 9 {
		t.Fatalf("options did not settle cfg: %+v", custom.cfg)
	}
	// The override must not leak into the package globals or another state.
	if MaxStack == 123 {
		t.Fatal("WithMaxStack mutated the package global")
	}
	if NewState().cfg.maxStack == 123 {
		t.Fatal("override leaked into a fresh state")
	}
}

// A coroutine inherits the parent state's per-state limits.
func TestNewStateOptionsCoroutineInherit(t *testing.T) {
	L := NewState(WithMaxStack(4242), WithMaxCCalls(11))
	co := L.newThread()
	if co.cfg != L.cfg {
		t.Fatalf("coroutine cfg %+v != parent %+v", co.cfg, L.cfg)
	}
}

// WithSandbox opens the safe libraries and removes load/loadfile/dofile, and
// NewSandbox is its shorthand.
func TestWithSandboxLibraries(t *testing.T) {
	for _, L := range []*LState{NewState(WithSandbox()), NewSandbox()} {
		if !L.GetGlobal("string").IsTable() {
			t.Fatal("sandbox should open string")
		}
		if !L.GetGlobal("os").IsNil() {
			t.Fatal("sandbox must not open os")
		}
		if !L.GetGlobal("load").IsNil() {
			t.Fatal("sandbox must remove load")
		}
	}
}

// The following behavioural tests prove each per-State limit actually changes
// runtime behaviour, and that two States with different limits behave
// differently in the SAME process — the whole point of per-State overrides,
// which the package-global model cannot do.

// WithMaxTableArraySize turns unbounded array growth into a catchable error for
// the capped State, while an unconfigured State fills the table fine.
func TestWithMaxTableArraySizeRuntime(t *testing.T) {
	const fill = `return pcall(function() local t = {} for i = 1, 100 do t[i] = i end end)`

	capped := NewState(WithOpenLibs(), WithMaxTableArraySize(8))
	res, err := capped.DoString(fill, "=cap")
	if err != nil {
		t.Fatalf("unexpected host error: %v", err)
	}
	if res[0].AsBool() {
		t.Fatal("capped state should have raised when filling past the cap")
	}
	if !strings.Contains(res[1].Str(), "not enough memory") {
		t.Fatalf("want 'not enough memory', got %q", res[1].Str())
	}

	free := NewState(WithOpenLibs()) // default cap 0 = unlimited
	res2, err := free.DoString(`local t = {} for i = 1, 100 do t[i] = i end return #t`, "=free")
	if err != nil {
		t.Fatalf("unexpected host error: %v", err)
	}
	if res2[0].AsInt() != 100 {
		t.Fatalf("unconfigured state want 100, got %d", res2[0].AsInt())
	}
}

// WithMaxStack makes deep (non-tail) recursion overflow for the capped State at
// a far smaller depth than the default ceiling permits.
func TestWithMaxStackRuntime(t *testing.T) {
	// 1 + f(n-1) keeps a pending add, so each call holds a frame (no tail call).
	const src = `local function f(n) if n == 0 then return 0 end return 1 + f(n - 1) end
	             return pcall(f, 8000)`

	capped := NewState(WithOpenLibs(), WithMaxStack(2000))
	res, err := capped.DoString(src, "=cap")
	if err != nil {
		t.Fatalf("unexpected host error: %v", err)
	}
	if res[0].AsBool() {
		t.Fatal("capped state should overflow before the target depth")
	}
	if !strings.Contains(res[1].Str(), "stack overflow") {
		t.Fatalf("want 'stack overflow', got %q", res[1].Str())
	}

	free := NewState(WithOpenLibs())
	res2, err := free.DoString(src, "=free")
	if err != nil {
		t.Fatalf("unexpected host error: %v", err)
	}
	if !res2[0].AsBool() {
		t.Fatalf("default state should reach the target depth, got error %q", res2[1].Str())
	}
}

// WithMaxCCalls bounds nested Go-level calls: each pcall is one such call, so
// recursing through pcall past the cap raises a catchable "C stack overflow"
// for the capped State while the default ceiling lets it reach the base case.
func TestWithMaxCCallsRuntime(t *testing.T) {
	// f propagates whatever the inner pcall yields, so the top result is either
	// "done" (base case reached) or the raised error message.
	const src = `local function f(n)
	                 if n == 0 then return "done" end
	                 local ok, res = pcall(f, n - 1)
	                 return res
	             end
	             return f(60)`

	capped := NewState(WithOpenLibs(), WithMaxCCalls(10))
	res, err := capped.DoString(src, "=cap")
	if err != nil {
		t.Fatalf("unexpected host error: %v", err)
	}
	if !strings.Contains(res[0].Str(), "C stack overflow") {
		t.Fatalf("want 'C stack overflow', got %q", res[0].Str())
	}

	free := NewState(WithOpenLibs())
	res2, err := free.DoString(src, "=free")
	if err != nil {
		t.Fatalf("unexpected host error: %v", err)
	}
	if res2[0].Str() != "done" {
		t.Fatalf("default state should reach the base case, got %q", res2[0].Str())
	}
}
