-- lua-pure PUC-conformance regression: assert / error / pcall / xpcall
--
-- The headline case: assert(false, msg) tail-calls error(), so a STRING
-- message gets the caller's position prepended (level 1) -- not just the
-- default "assertion failed!". lua-pure previously raised a custom string
-- message verbatim. Non-string messages (and explicit error levels) pass
-- through unchanged. Pinned to PUC lua5.4.8.

local function eq(got, want, desc)
  assert(got == want,
    string.format("aep [%s]: got %s, want %s", desc, tostring(got), tostring(want)))
end
local function caught(f)
  local ok, msg = pcall(f)
  return ok, msg
end

-- assert: a string message is position-prefixed (like error at level 1)
do
  local ok, msg = caught(function() assert(false, "custom") end)
  eq(ok, false, "assert string errors")
  assert(type(msg) == "string" and msg:match(":%d+: custom$") ~= nil,
    "assert position prefix: " .. tostring(msg))
end
-- assert default message is also position-prefixed
do
  local ok, msg = caught(function() assert(nil) end)
  eq(ok, false, "assert default errors")
  assert(msg:match(":%d+: assertion failed!$") ~= nil, "assert default: " .. tostring(msg))
end
-- a non-string assert message passes through by identity (no position)
do
  local obj = {code = 1}
  local ok, msg = caught(function() assert(false, obj) end)
  eq(msg, obj, "assert object message identity")
  local ok2, n = caught(function() assert(false, 42) end)
  eq(n, 42, "assert number message verbatim")
end
-- assert returns all its arguments on success
do
  local a, b, c = assert(10, "x", true)
  eq(a, 10, "assert passthrough 1"); eq(b, "x", "assert passthrough 2"); eq(c, true, "assert passthrough 3")
end

-- error: level 1 prefixes position, level 0 does not, object passes through
do
  local _, m1 = caught(function() error("e") end)
  assert(m1:match(":%d+: e$") ~= nil, "error level 1: " .. m1)
  local _, m0 = caught(function() error("e", 0) end)
  eq(m0, "e", "error level 0 no prefix")
  local _, mo = caught(function() error({c = 9}) end)
  eq(type(mo), "table", "error object type"); eq(mo.c, 9, "error object preserved")
  local _, mn = caught(function() error() end)
  eq(mn, nil, "error() with no arg is nil")
end

-- pcall: multiple returns, extra args forwarded, calling a non-function fails
do
  local ok, a, b, c = pcall(function() return 1, 2, 3 end)
  eq(ok, true, "pcall ok"); eq(a + b + c, 6, "pcall multi-return")
  local ok2, sum = pcall(function(x, y) return x + y end, 10, 20)
  eq(ok2, true, "pcall args ok"); eq(sum, 30, "pcall forwards args")
  eq((pcall(42)), false, "pcall non-function is false")
end

-- xpcall: handler runs on error and receives the (already-prefixed) message
do
  local ok, r = xpcall(function() error("E") end, function(m) return "H:" .. (m:gsub("^[^:]*:%d+: ", "")) end)
  eq(ok, false, "xpcall errored"); eq(r, "H:E", "xpcall handler result")
  local ok2, r2 = xpcall(function(a, b) return a * b end, function() return "h" end, 6, 7)
  eq(ok2, true, "xpcall ok with args"); eq(r2, 42, "xpcall forwards args")
end

print("assert-error-pcall: all cases passed")
