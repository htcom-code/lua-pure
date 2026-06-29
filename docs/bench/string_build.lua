-- string_build: format many small strings and concatenate them.
-- Count is sized so the loop runs ~100ms+ per pass: short runs let fixed costs
-- (state init, first GC cycle, os.clock resolution) inflate the cross-engine
-- ratio, so this measures steady-state throughput.
local function work()
  local t = {}
  for i = 1, 1200000 do
    t[i] = string.format("item-%d=%d", i, (i * i) % 1000)
  end
  local joined = table.concat(t, ",")
  return #joined
end

local best = math.huge
for _ = 1, 7 do
  local t0 = os.clock()
  local r = work()
  local dt = os.clock() - t0
  if dt < best then best = dt end
end
io.write(string.format("%.5f", best))
