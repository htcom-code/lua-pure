-- lua-pure PUC-conformance regression: debug introspection
--
-- The headline case: debug.getupvalue / debug.setupvalue return NO values
-- for an out-of-range index (PUC db_getupvalue/db_setupvalue `return 0`),
-- whereas debug.getlocal / debug.setlocal return a single nil. lua-pure
-- previously returned nil from get/setupvalue too. Also pins getinfo field
-- selection, locals, and upvalue identity/join. Pinned to PUC lua5.4.8.

local function eq(got, want, desc)
  assert(got == want,
    string.format("dbg [%s]: got %s, want %s", desc, tostring(got), tostring(want)))
end

-- out-of-range return arity: getupvalue/setupvalue -> 0, getlocal/setlocal -> 1
do
  local up = 1
  local function f() return up end
  eq(select("#", debug.getupvalue(f, 5)), 0, "getupvalue oob returns nothing")
  eq(select("#", debug.setupvalue(f, 5, 9)), 0, "setupvalue oob returns nothing")
  eq(select("#", debug.getupvalue(f, 1)), 2, "getupvalue ok returns name+value")
end
do
  local function g(a) return select("#", debug.getlocal(1, 50)) end
  eq(g(1), 1, "getlocal oob returns a single nil")
  local function h(a) return select("#", debug.setlocal(1, 50, 9)) end
  eq(h(1), 1, "setlocal oob returns a single value")
end

-- getinfo field selection
local function sample(a, b) local x = a + b return x end
eq(debug.getinfo(sample, "S").what, "Lua", "getinfo what Lua")
eq(debug.getinfo(print, "S").what, "C", "getinfo what C")
do
  local i = debug.getinfo(sample, "u")
  eq(i.nparams, 2, "getinfo nparams"); eq(i.isvararg, false, "getinfo isvararg"); eq(i.nups, 0, "getinfo nups")
end
eq(debug.getinfo(sample, "f").func, sample, "getinfo func field")
eq(type(debug.getinfo(sample, "L").activelines), "table", "getinfo activelines")
eq(debug.getinfo(1, "S").what, "main", "getinfo main chunk what")

-- getlocal / setlocal on parameters and locals
do
  local function f(a, b) local n, v = debug.getlocal(1, 1) return n .. "=" .. v end
  eq(f(7, 8), "a=7", "getlocal param 1")
  local function g(a) debug.setlocal(1, 1, 99) return a end
  eq(g(1), 99, "setlocal param")
end

-- getupvalue / setupvalue
do
  local up = 5
  local function f() return up end
  local n, v = debug.getupvalue(f, 1)
  eq(n .. "=" .. v, "up=5", "getupvalue")
  debug.setupvalue(f, 1, 50)
  eq(f(), 50, "setupvalue")
end

-- upvalueid identity: two closures over the same upvalue share an id
do
  local x = 1
  local function f() return x end
  local function g() return x end
  eq(debug.upvalueid(f, 1) == debug.upvalueid(g, 1), true, "shared upvalue same id")
  local function mk() local y = 1 return function() return y end end
  eq(debug.upvalueid(mk(), 1) == debug.upvalueid(mk(), 1), false, "distinct upvalues differ")
end

-- upvaluejoin makes one closure share another's upvalue cell
do
  local function mk(v) local y = v return function() return y end end
  local f, g = mk(1), mk(2)
  debug.upvaluejoin(f, 1, g, 1)
  eq(f(), 2, "upvaluejoin shares cell")
end

-- traceback and metatable accessors
eq(debug.traceback("msg"):match("^msg") ~= nil, true, "traceback keeps message")
eq(type(debug.traceback()), "string", "traceback no arg")
do
  local t = {}
  eq(debug.traceback(t), t, "traceback passes non-string through")
end
eq(debug.getmetatable("") ~= nil, true, "string has a metatable")
eq(debug.getmetatable(5), nil, "number has no metatable")
eq(type(debug.getregistry()), "table", "getregistry is a table")

print("debug-introspection: all cases passed")
