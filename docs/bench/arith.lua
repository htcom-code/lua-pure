-- arith: a tight numeric loop (float math, common to 5.1 and 5.4).
local function work()
  local s = 0.0
  for i = 1, 4000000 do
    s = s + i * 1.5 - (i % 7) + i / 3.0
  end
  return s
end

local best = math.huge
for _ = 1, 7 do
  local t0 = os.clock()
  local r = work()
  local dt = os.clock() - t0
  if dt < best then best = dt end
end
io.write(string.format("%.5f", best))
