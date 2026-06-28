package luapure_test

import (
	"strings"
	"testing"

	luapure "github.com/htcom-code/lua-pure/lua"
)

// CallProtoEnv runs a cached *Proto with a per-call _ENV: a fresh env isolates
// each call's globals, a reused env carries them. This is the compile-once +
// env-swap sandbox primitive for a pool (no recompile per call).
func TestCallProtoEnvPerCallIsolation(t *testing.T) {
	L := luapure.NewState(luapure.WithOpenLibs())
	p, err := luapure.CompileString(`count = (count or 0) + 1; return count`, "=p")
	if err != nil {
		t.Fatal(err)
	}

	// Fresh env each call: always 1 (globals confined to that call).
	for i := 0; i < 3; i++ {
		res, err := L.CallProtoEnv(p, luapure.NewTable(), -1)
		if err != nil {
			t.Fatal(err)
		}
		if got := res[0].AsInt(); got != 1 {
			t.Fatalf("fresh env: call %d leaked, got %d", i, got)
		}
	}

	// Reused env: globals accumulate.
	env := luapure.NewTable()
	for want := int64(1); want <= 3; want++ {
		res, err := L.CallProtoEnv(p, env, -1)
		if err != nil {
			t.Fatal(err)
		}
		if got := res[0].AsInt(); got != want {
			t.Fatalf("reused env: call %d got %d", want, got)
		}
	}
}

// SetInstructionLimit aborts a runaway pure-Lua loop with a catchable error,
// while a program under the cap runs fine; the limit resets per set.
func TestInstructionLimit(t *testing.T) {
	L := luapure.NewState(luapure.WithOpenLibs())

	L.SetInstructionLimit(200000)
	_, err := L.DoString(`while true do end`, "=spin")
	if err == nil || !strings.Contains(err.Error(), "instruction limit exceeded") {
		t.Fatalf("want instruction-limit error, got %v", err)
	}

	// Reset and run a small program: must complete (under the cap, never gated).
	L.SetInstructionLimit(200000)
	res, err := L.DoString(`return 1 + 1`, "=ok")
	if err != nil {
		t.Fatalf("under-cap program errored: %v", err)
	}
	if res[0].AsInt() != 2 {
		t.Fatalf("got %d", res[0].AsInt())
	}

	// Clearing removes the cap.
	L.ClearInstructionLimit()
	if L.InstructionCount() != 0 {
		t.Fatalf("count should be 0 after clear, got %d", L.InstructionCount())
	}
}

// A coroutine draws from the SAME instruction budget as its parent, so a script
// cannot multiply its allowance by spinning inside a spawned coroutine.
func TestInstructionLimitCoroutineShared(t *testing.T) {
	L := luapure.NewState(luapure.WithOpenLibs())
	L.SetInstructionLimit(200000)
	// resume catches the coroutine's error and returns (false, message); if the
	// budget were NOT shared, the inner loop would never stop and this would hang.
	res, err := L.DoString(`
		local co = coroutine.create(function() while true do end end)
		return coroutine.resume(co)
	`, "=co")
	if err != nil {
		t.Fatalf("unexpected host error: %v", err)
	}
	if res[0].AsBool() {
		t.Fatal("coroutine should have failed under the shared instruction budget")
	}
	if !strings.Contains(res[1].Str(), "instruction limit exceeded") {
		t.Fatalf("want instruction-limit message, got %q", res[1].Str())
	}
}
