package luapure

import "testing"

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
