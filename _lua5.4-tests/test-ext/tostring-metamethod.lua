-- lua-pure PUC-conformance regression: __tostring return-value handling
--
-- luaL_tolstring accepts whatever lua_isstring accepts from a __tostring
-- metamethod: a string OR a number (coerced to its decimal). Only a
-- non-string/number return raises "'__tostring' must return a string".
-- lua-pure previously rejected a numeric return outright. Every `want`
-- was captured from PUC lua5.4.8.

local function eq(got, want, desc)
  assert(got == want,
    string.format("tostring-meta [%s]: got %s, want %s", desc, tostring(got), tostring(want)))
end

local function withTS(fn) return setmetatable({}, {__tostring = fn}) end

-- a string return is used verbatim
eq(tostring(withTS(function() return "hi" end)), "hi", "string return")

-- a number return coerces to its decimal (the previously-broken case)
eq(tostring(withTS(function() return 42 end)), "42", "integer return -> decimal")
eq(tostring(withTS(function() return 4.5 end)), "4.5", "float return -> decimal")

-- a non-string/number return errors with PUC's exact message
local function errtail(v)
  local ok, msg = pcall(tostring, v)
  assert(not ok, "expected error")
  return (msg:gsub("^[^:]*:%d+: ", ""))
end
eq(errtail(withTS(function() return true end)), "'__tostring' must return a string", "bool return errors")
eq(errtail(withTS(function() return {} end)), "'__tostring' must return a string", "table return errors")
eq(errtail(withTS(function() return nil end)), "'__tostring' must return a string", "nil return errors")

print("tostring-metamethod: all cases passed")
