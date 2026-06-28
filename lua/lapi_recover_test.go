package luapure

import (
	"errors"
	"strings"
	"testing"
)

// Protected mode: a raw Go panic from a callback is recovered into a catchable
// *GoPanicError (not escaped to the host), and the State stays reusable.
func TestRecoverGoPanics_CaughtAndReusable(t *testing.T) {
	L := NewState(WithOpenLibs(), WithRecoverGoPanics())
	L.Register("boom", func(L *LState) int { panic("kaboom") })

	_, err := L.DoString(`return boom()`, "=p")
	var gpe *GoPanicError
	if !errors.As(err, &gpe) {
		t.Fatalf("want *GoPanicError, got %T: %v", err, err)
	}
	if gpe.Value != "kaboom" {
		t.Fatalf("panic value lost: %v", gpe.Value)
	}

	res, err := L.DoString(`return 1 + 1`, "=after")
	if err != nil || res[0].AsInt() != 2 {
		t.Fatalf("state not reusable after caught panic: %v", err)
	}
}

// The unwind must restore the VM to its pre-call baseline even when the panic
// fires under nested frames with locals/upvalues — proving reuse is sound, not
// lucky. Checks the same fields pcall restores (top/openuv/tbc).
func TestRecoverGoPanics_UnwindRestoresInternals(t *testing.T) {
	L := NewState(WithOpenLibs(), WithRecoverGoPanics())
	L.Register("boom", func(L *LState) int { panic("x") })

	top0, uv0, tbc0 := L.top, len(L.openuv), len(L.tbc)
	_, err := L.DoString(`local a, b, c = 1, 2, 3
		local function f() return boom() end
		return f()`, "=p")
	if err == nil {
		t.Fatal("expected an error")
	}
	if L.top != top0 || len(L.openuv) != uv0 || len(L.tbc) != tbc0 {
		t.Fatalf("internals not restored: top %d->%d, openuv %d->%d, tbc %d->%d",
			top0, L.top, uv0, len(L.openuv), tbc0, len(L.tbc))
	}
}

// Default (no option): a raw Go panic re-panics to the host, unchanged — the
// PUC-faithful behaviour.
func TestRecoverGoPanics_DefaultRepanics(t *testing.T) {
	L := NewState(WithOpenLibs())
	L.Register("boom", func(L *LState) int { panic("x") })
	defer func() {
		if recover() == nil {
			t.Fatal("default mode must re-panic a Go callback panic")
		}
	}()
	_, _ = L.DoString(`return boom()`, "=p")
}

// A proper Lua error (ArgError) is unaffected by protected mode: it is a
// *LuaError, never a *GoPanicError.
func TestRecoverGoPanics_LuaErrorNotMisclassified(t *testing.T) {
	L := NewState(WithOpenLibs(), WithRecoverGoPanics())
	L.Register("boom", func(L *LState) int { L.ArgError(1, "bad"); return 0 })

	_, err := L.DoString(`return boom()`, "=p")
	var gpe *GoPanicError
	if errors.As(err, &gpe) {
		t.Fatal("a Lua error must not surface as *GoPanicError")
	}
	var le *LuaError
	if !errors.As(err, &le) {
		t.Fatalf("want *LuaError, got %T", err)
	}
}

// In protected mode a script-level pcall catches the recovered Go panic as a
// normal error value (its message), so scripts behave consistently.
func TestRecoverGoPanics_ScriptPcallCatches(t *testing.T) {
	L := NewState(WithOpenLibs(), WithRecoverGoPanics())
	L.Register("boom", func(L *LState) int { panic("kaboom") })

	res, err := L.DoString(`return pcall(boom)`, "=p")
	if err != nil {
		t.Fatalf("unexpected host error: %v", err)
	}
	if res[0].AsBool() {
		t.Fatal("script pcall should report failure")
	}
	if !strings.Contains(res[1].Str(), "kaboom") {
		t.Fatalf("error value missing panic info: %q", res[1].Str())
	}
}

// A Go panic inside a coroutine body, in protected mode, is contained: it does
// not escape to the host process. It surfaces at the host protected boundary as
// a *GoPanicError rather than being returned by coroutine.resume (the resume
// path re-raises non-LuaError, so the enclosing host pcall catches it). Documents
// the v1 limitation: a coroutine's Go-callback panic is not caught by resume.
func TestRecoverGoPanics_CoroutineContainedAtHost(t *testing.T) {
	L := NewState(WithOpenLibs(), WithRecoverGoPanics())
	L.Register("boom", func(L *LState) int { panic("kaboom") })

	escaped := false
	func() {
		defer func() {
			if recover() != nil {
				escaped = true
			}
		}()
		_, err := L.DoString(`
			local co = coroutine.create(function() boom() end)
			return coroutine.resume(co)
		`, "=co")
		var gpe *GoPanicError
		if !errors.As(err, &gpe) {
			t.Fatalf("want host-caught *GoPanicError, got %v", err)
		}
	}()
	if escaped {
		t.Fatal("a coroutine Go panic must not escape the process in protected mode")
	}
}
