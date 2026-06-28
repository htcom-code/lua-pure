package luapure

import "testing"

// __gc runs when an object becomes unreachable and a collection is forced.
func TestFinalizerRunsOnCollect(t *testing.T) {
	if !runBool(t, `
		local finish = false
		do local u = setmetatable({}, {__gc = function() finish = true end}); u = nil end
		collectgarbage()
		return finish
	`) {
		t.Error("__gc did not run on collectgarbage")
	}
}

// A loop spinning on a finalizer side effect terminates: the dead object is
// reclaimed under the loop's allocation pressure and the VM poll drains __gc.
// This is gc.lua's GC1 idiom.
func TestFinalizerSpinLoopTerminates(t *testing.T) {
	if !runBool(t, `
		local finish = false
		local u = setmetatable({}, {__gc = function() finish = true end})
		u = nil
		local n = 0
		repeat u = {}; n = n + 1; if n > 50000000 then break end until finish
		return finish
	`) {
		t.Error("finalizer spin loop did not terminate (finish stayed false)")
	}
}

// The finalizer receives the object itself (resurrection): it can read its
// fields during __gc.
func TestFinalizerReceivesObject(t *testing.T) {
	if !runBool(t, `
		local seen = nil
		do
			local u = setmetatable({tag = 42}, {__gc = function(o) seen = o.tag end})
			u = nil
		end
		collectgarbage()
		return seen == 42
	`) {
		t.Error("__gc did not receive the object with its fields intact")
	}
}

// An error raised inside __gc is demoted, never propagated to the mutator
// (PUC luaE_warnerror).
func TestFinalizerErrorSwallowed(t *testing.T) {
	if !runBool(t, `
		do local u = setmetatable({}, {__gc = function() error("boom") end}); u = nil end
		local ok = pcall(collectgarbage)
		return ok   -- collectgarbage must not raise the __gc error
	`) {
		t.Error("error in __gc was propagated instead of swallowed")
	}
}

// __gc runs at most once even across repeated collections.
func TestFinalizerRunsOnce(t *testing.T) {
	if !runBool(t, `
		local count = 0
		do local u = setmetatable({}, {__gc = function() count = count + 1 end}); u = nil end
		collectgarbage()
		collectgarbage()
		collectgarbage()
		return count == 1
	`) {
		t.Error("__gc ran more than once")
	}
}
