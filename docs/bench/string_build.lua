-- string_build: format many small strings and concatenate them.
local function work()
  local t = {}
  for i = 1, 120000 do
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
