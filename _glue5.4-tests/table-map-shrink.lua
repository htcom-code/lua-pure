-- gopher-lua bugfix regression: hash-part map shrink keeps data intact
--
-- The fork rebuilds a table's underlying Go map after heavy deletion to
-- release retained bucket memory (issue #214). This must not corrupt the
-- surviving entries or iteration. Pure-correctness checks (the memory
-- release itself is asserted by a Go test); behaviour matches PUC Lua 5.1.
-- Reusable on lua54-dev.

local function eq(got, want, desc)
  assert(got == want,
    string.format("table-map-shrink [%s]: got %s, want %s", desc, tostring(got), tostring(want)))
end

-- string keys: fill past the small-slice promotion, churn down hard, verify
do
  local t = {}
  for i = 1, 2000 do t["k" .. i] = i end
  for i = 1, 1900 do t["k" .. i] = nil end -- delete 95% -> triggers shrink
  local n = 0
  for k, v in pairs(t) do
    n = n + 1
    eq(t[k], v, "string survivor " .. k)
  end
  eq(n, 100, "string live count after churn")
  eq(t.k1901, 1901, "specific survivor k1901")
  eq(t.k2000, 2000, "specific survivor k2000")
  eq(t.k1, nil, "deleted k1 is nil")
  -- re-add after shrink still works
  t.k1 = -1
  eq(t.k1, -1, "re-add after shrink")
end

-- non-string keys (dict path): boolean/number-as-key churn
do
  local t = {}
  for i = 1, 2000 do t[i + 0.5] = i end -- float keys -> hash dict
  for i = 1, 1900 do t[i + 0.5] = nil end
  local n = 0
  for k, v in pairs(t) do n = n + 1; eq(t[k], v, "dict survivor") end
  eq(n, 100, "dict live count after churn")
  eq(t[1901.5], 1901, "dict survivor 1901.5")
  eq(t[1.5], nil, "deleted dict key nil")
end

-- full clear then reuse (the empty-release path)
do
  local t = {}
  for i = 1, 500 do t["x" .. i] = i end
  for i = 1, 500 do t["x" .. i] = nil end
  local n = 0
  for _ in pairs(t) do n = n + 1 end
  eq(n, 0, "fully cleared")
  t.fresh = 7
  eq(t.fresh, 7, "reuse after full clear")
end

print("table-map-shrink: all cases passed")
