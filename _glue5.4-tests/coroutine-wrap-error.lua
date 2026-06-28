-- lua-pure PUC-conformance regression: coroutine.wrap error propagation
--
-- luaB_auxwrap re-raises a coroutine error in the caller, and for a STRING
-- error object it prepends luaL_where(1) -- the position of the site that
-- called the wrapper. A non-string error object propagates unchanged.
-- lua-pure previously re-raised the raw error without that prefix, dropping
-- the caller's position. Behaviour pinned to PUC lua5.4.8.

local function eq(got, want, desc)
  assert(got == want,
    string.format("co-wrap [%s]: got %s, want %s", desc, tostring(got), tostring(want)))
end

-- A string error gets the caller's position prepended on top of the error's
-- own position, so a wrapper called directly from Lua yields TWO "src:line: "
-- prefixes before the message.
do
  local w = coroutine.wrap(function() error("boom") end)
  local ok, msg = pcall(function() w() end) -- direct Lua call site
  eq(ok, false, "errors")
  assert(type(msg) == "string", "co-wrap [string msg]: type " .. type(msg))
  -- two position prefixes (caller's, then the error()'s own), then "boom"
  assert(msg:match("^.-:%d+: .-:%d+: boom$") ~= nil,
    "co-wrap [double prefix]: got " .. msg)
end

-- error(level 0) carries no position of its own, so only the wrapper caller's
-- position is prepended: exactly one "src:line: " prefix.
do
  local w = coroutine.wrap(function() error("nolevel", 0) end)
  local ok, msg = pcall(function() w() end)
  eq(ok, false, "level0 errors")
  assert(msg:match("^.-:%d+: nolevel$") ~= nil, "co-wrap [single prefix]: got " .. msg)
end

-- a non-string error object propagates unchanged (no string surgery)
do
  local obj = {code = 7}
  local w = coroutine.wrap(function() error(obj) end)
  local ok, msg = pcall(function() w() end)
  eq(ok, false, "obj errors")
  eq(msg, obj, "object identity preserved")
end

-- a normal wrap (no error) yields values across resumes
do
  local gen = coroutine.wrap(function() for i = 1, 3 do coroutine.yield(i * 10) end end)
  eq(gen() + gen() + gen(), 60, "yield sequence")
end

print("coroutine-wrap-error: all cases passed")
