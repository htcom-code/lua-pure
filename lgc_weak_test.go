package luapure

import "testing"

// runBool runs a full chunk (with the standard libraries open, so collectgarbage
// is available) and returns its single boolean result.
func runBool(t *testing.T, src string) bool {
	t.Helper()
	L := NewState()
	L.OpenLibs()
	res, err := L.DoString(src, "=weaktest")
	if err != nil {
		t.Fatalf("DoString error: %v", err)
	}
	if len(res) != 1 || !res[0].IsBool() {
		t.Fatalf("want one boolean result, got %v", res)
	}
	return res[0].AsBool()
}

// A weak-value table drops an entry once its referent is unreachable and a
// collection runs — the closure.lua / gengc.lua behavior, in miniature.
func TestWeakValueCleared(t *testing.T) {
	if !runBool(t, `
		local t = setmetatable({}, {__mode = "v"})
		t[1] = {10}            -- only reference to the inner table
		collectgarbage()
		return t[1] == nil
	`) {
		t.Error("weak value was not cleared after collectgarbage")
	}
}

// __mode = "kv" set *after* the value is stored must still convert and clear it
// (closure.lua sets the value before setmetatable).
func TestWeakModeAppliedAfterInsert(t *testing.T) {
	if !runBool(t, `
		local t = {[1] = {}}
		setmetatable(t, {__mode = "kv"})
		local n = 0
		while t[1] do            -- spins until GC reclaims the weak value
			local s = tostring(n) .. tostring(n)
			n = n + 1
			if n % 1000 == 0 then collectgarbage() end
			if n > 1e6 then break end
		end
		return t[1] == nil
	`) {
		t.Error("weak value stored before setmetatable was not cleared")
	}
}

// A strong (non-weak) table must retain its entries across a collection.
func TestStrongTableRetains(t *testing.T) {
	if !runBool(t, `
		local t = {}
		t[1] = {10}
		collectgarbage()
		return t[1] ~= nil and t[1][1] == 10
	`) {
		t.Error("strong table lost an entry across collectgarbage")
	}
}

// __mode round-trips through getmetatable unchanged.
func TestWeakModeRoundTrip(t *testing.T) {
	if !runBool(t, `
		local t = setmetatable({}, {__mode = "kv"})
		return getmetatable(t).__mode == "kv"
	`) {
		t.Error("__mode did not round-trip")
	}
}

// A live referent held elsewhere must survive in a weak table.
func TestWeakValueSurvivesWhileReferenced(t *testing.T) {
	if !runBool(t, `
		local keep = {10}
		local t = setmetatable({}, {__mode = "v"})
		t[1] = keep
		collectgarbage()
		return t[1] == keep and t[1][1] == 10
	`) {
		t.Error("weak value with a live strong reference was wrongly cleared")
	}
}
