-- tree_build: build a balanced binary tree from a flat node map, then DFS-sum.
-- Pure in-memory table work. Common Lua 5.1/5.4 syntax.
-- N is sized so the loop runs ~100ms+ per pass: short runs let fixed costs
-- (state init, first GC cycle, os.clock resolution) inflate the cross-engine
-- ratio, so this measures steady-state throughput.
local function work()
  local N = 200000
  local nodes = {}
  for i = 1, N do
    nodes[i] = { i, (i > 1) and math.floor(i / 2) or 0, (i * 2654435761) % 1000003, {} }
  end
  local roots, nroots = {}, 0
  for i = 1, N do
    local n = nodes[i]
    if n[2] == 0 then
      nroots = nroots + 1
      roots[nroots] = n
    else
      local pc = nodes[n[2]][4]
      pc[#pc + 1] = n
    end
  end
  local total = 0
  local snode, sdepth, top = {}, {}, 0
  for r = 1, nroots do
    top = top + 1; snode[top] = roots[r]; sdepth[top] = 1
  end
  while top > 0 do
    local node = snode[top]; top = top - 1
    total = total + node[3]
    local ch = node[4]
    for k = 1, #ch do top = top + 1; snode[top] = ch[k] end
  end
  return total
end

local best = math.huge
for _ = 1, 7 do
  local t0 = os.clock()
  local r = work()
  local dt = os.clock() - t0
  if dt < best then best = dt end
end
io.write(string.format("%.5f", best))
