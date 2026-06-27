-- lua-pure PUC-conformance regression: metamethods
--
-- Covers the table/operator metamethods, with the headline 5.4 case being
-- __le emulation: stock Lua 5.4.8 ships luaconf.h with LUA_COMPAT_LT_LE
-- defined, so 'a <= b' with only '__lt' is emulated as '!(b < a)'. lua-pure
-- previously errored ("attempt to compare two table values"). Behaviour
-- pinned to PUC lua5.4.8.

local function eq(got, want, desc)
  assert(got == want,
    string.format("mm [%s]: got %s, want %s", desc, tostring(got), tostring(want)))
end
local function errtail(f)
  local ok, msg = pcall(f)
  assert(not ok, "expected error")
  return (tostring(msg):gsub("^[^:]*:%d+: ", ""))
end

-- __index: function, table, and a 3-level chain; rawget bypasses it
eq(setmetatable({}, {__index = function(_, k) return "got:" .. k end}).foo, "got:foo", "__index func")
eq((function()
  local a = {v = "deep"}
  local b = setmetatable({}, {__index = a})
  local c = setmetatable({}, {__index = b})
  return c.v
end)(), "deep", "__index chain")
eq(rawget(setmetatable({}, {__index = function() return "m" end}), "k"), nil, "rawget bypasses __index")
eq(setmetatable({real = 9}, {__index = function() return "X" end}).real, 9, "present key skips __index")

-- __newindex: function and table targets; an existing key skips it; rawset bypasses
eq((function()
  local store = {}
  local t = setmetatable({}, {__newindex = store})
  t.k = 7
  return store.k .. "/" .. tostring(rawget(t, "k"))
end)(), "7/nil", "__newindex table")
eq((function()
  local t = setmetatable({x = 1}, {__newindex = function() error("must not fire") end})
  t.x = 99
  return t.x
end)(), 99, "__newindex skipped for existing key")

-- __call
eq(setmetatable({n = 10}, {__call = function(self, x) return self.n + x end})(5), 15, "__call")
eq(errtail(function() return ({})() end), "attempt to call a table value", "call without __call errors")

-- __concat on either side, and chained
eq("x" .. setmetatable({}, {__concat = function() return "C" end}), "C", "__concat rhs")
eq((function()
  local mt = {__concat = function(a, b)
    return (type(a) == "table" and "T" or a) .. (type(b) == "table" and "T" or b)
  end}
  return "a" .. setmetatable({}, mt) .. "b"
end)(), "aTb", "__concat chained")

-- __len, __unm, __add (both operand sides)
eq(#setmetatable({}, {__len = function() return 42 end}), 42, "__len")
eq(-setmetatable({}, {__unm = function() return "neg" end}), "neg", "__unm")
eq(5 + setmetatable({}, {__add = function() return 200 end}), 200, "__add rhs")

-- __eq fires only between two tables; comparing to another type is just false
eq(setmetatable({}, {__eq = function() return true end}) == setmetatable({}, {__eq = function() return true end}), true, "__eq")
eq(setmetatable({}, {__eq = function() return true end}) == 5, false, "__eq cross-type is false")

-- __lt, __le, and the LUA_COMPAT_LT_LE emulation of __le via __lt
local lt = {__lt = function(a, b) return a.v < b.v end}
eq(setmetatable({v = 1}, lt) < setmetatable({v = 2}, lt), true, "__lt")
eq(setmetatable({v = 2}, lt) <= setmetatable({v = 3}, lt), true, "__le via __lt: 2<=3")
eq(setmetatable({v = 3}, lt) <= setmetatable({v = 2}, lt), false, "__le via __lt: 3<=2")
eq(setmetatable({v = 2}, lt) <= setmetatable({v = 2}, lt), true, "__le via __lt: 2<=2 (equal)")
eq(setmetatable({v = 3}, lt) >= setmetatable({v = 2}, lt), true, "__ge via __lt emulation")
-- a real __le takes precedence and is used directly
eq(setmetatable({v = 2}, {__le = function(a, b) return a.v <= b.v end}) <=
   setmetatable({v = 2}, {__le = function(a, b) return a.v <= b.v end}), true, "explicit __le")
-- neither __le nor __lt: still an error
eq(errtail(function() return setmetatable({}, {}) <= setmetatable({}, {}) end),
   "attempt to compare two table values", "no __le/__lt errors")

-- __metatable hides and protects the real metatable
eq((function()
  local t = setmetatable({}, {__metatable = "locked"})
  return tostring(getmetatable(t)) .. "/" .. tostring(pcall(setmetatable, t, {}))
end)(), "locked/false", "__metatable protection")

-- __close runs in reverse declaration order, and runs on error unwinding
eq((function()
  local order = {}
  do
    local a <close> = setmetatable({}, {__close = function() order[#order + 1] = "a" end})
    local b <close> = setmetatable({}, {__close = function() order[#order + 1] = "b" end})
  end
  return table.concat(order, ",")
end)(), "b,a", "__close reverse order")
eq((function()
  local closed = false
  pcall(function()
    local r <close> = setmetatable({}, {__close = function() closed = true end})
    error("boom")
  end)
  return closed
end)(), true, "__close runs on error")
eq(errtail(function() local x <close> = setmetatable({}, {}) end),
   "variable 'x' got a non-closable value", "non-closable <close> errors")

print("metamethods: all cases passed")
