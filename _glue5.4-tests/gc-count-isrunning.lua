-- collectgarbage "count" is a number and isrunning tracks stop/restart.
-- Oracle: PUC Lua 5.4.8. (luapure delegates collection to Go, but these
-- observable contracts still hold.)
assert(type(collectgarbage("count")) == "number")

collectgarbage("stop")
assert(collectgarbage("isrunning") == false)
collectgarbage("restart")
assert(collectgarbage("isrunning") == true)

local m = collectgarbage("count")
local t = {}
for i = 1, 10000 do t[i] = {i} end   -- live data
assert(collectgarbage("count") >= m) -- memory in use did not drop while t is live
assert(#t == 10000)                  -- keep t reachable past the measurement

print("gc-count-isrunning: all cases passed")
