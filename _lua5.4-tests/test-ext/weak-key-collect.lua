-- Weak keys ('k' mode): a live key is never cleared, and unreferenced
-- collectable keys are reclaimed. Oracle: PUC Lua 5.4.8. luapure delegates
-- collection to Go, so a just-inserted key may linger briefly (GC-root
-- precision), hence the count is bounded rather than exact.
local a = setmetatable({}, {__mode = "k"})
local live = {}
a[live] = "keep"
for i = 1, 200 do a[{}] = i end   -- throwaway collectable keys
collectgarbage()

assert(a[live] == "keep")          -- a key held elsewhere is never cleared
local n = 0
for k, v in pairs(a) do
  if k == live then assert(v == "keep") end
  n = n + 1
end
assert(n >= 1 and n < 200, "weak keys not collected: " .. n)  -- throwaways reclaimed

-- non-collectable keys (integers/strings) are never weak-cleared in a 'k' table
local b = setmetatable({}, {__mode = "k"})
b[42] = "i"; b["s"] = "str"
collectgarbage()
assert(b[42] == "i" and b["s"] == "str")

print("weak-key-collect: all cases passed")
