-- gopher-lua bugfix regression: register-assignment cardinality
--
-- compileRegAssignment counted left-hand targets with len(names) instead of
-- the real nvars, so a generic-for over a reused stateless iterator produced
-- wrong bytecode and the second loop saw nothing (issue #514). Verifies the
-- iterator-reuse case plus ordinary multi-assignment cardinality.
-- Backs upstream yuin/gopher-lua#528. Reusable on lua54-dev.

local function eq(got, want, desc)
  assert(got == want,
    string.format("multi-assign [%s]: got %s, want %s", desc, tostring(got), tostring(want)))
end

-- issue #514: a stateless iterator reused across two generic-for loops
do
  local tbl = {foo = 42, bar = "baz"}
  local iter = function(s, k)
    k = next(tbl, k)
    return k
  end
  local seen1, seen2 = {}, {}
  for item in iter do seen1[item] = true end
  for item in iter do seen2[item] = true end
  assert(seen1.foo and seen1.bar, "multi-assign [reuse loop 1]")
  assert(seen2.foo and seen2.bar, "multi-assign [reuse loop 2]")
end

-- extra left-hand names get nil
do
  local a, b, c = 1, 2
  eq(a, 1, "a"); eq(b, 2, "b"); eq(c, nil, "c is nil")
end

-- extra right-hand exprs are discarded
do
  local function f() return 1, 2, 3 end
  local a, b = f()
  eq(a, 1, "a from f"); eq(b, 2, "b from f")
end

-- vararg fans out to the remaining names
do
  local function g(...) local a, b, c = ...; return a, b, c end
  local x, y, z = g(7, 8)
  eq(x, 7, "vararg x"); eq(y, 8, "vararg y"); eq(z, nil, "vararg z nil")
end

print("compiler-multi-assign: all cases passed")
