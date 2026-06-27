-- os.time date-table field validation (getfield bounds), oracle PUC Lua 5.4.8.
local function expectErr(msg, ...)
  local ok, e = pcall(os.time, ...)
  assert(not ok and string.find(e, msg, 1, true),
         "expected '" .. msg .. "', got: " .. tostring(e))
end
expectErr("missing", {hour = 12})                                  -- no year/month/day
expectErr("not an integer", {year = 1000, month = 1, day = 1, hour = 'x'})
expectErr("not an integer", {year = 1000, month = 1, day = 1, hour = 1.5})
if string.packsize("i") == 4 then  -- 4-byte int fields
  expectErr("field 'year' is out-of-bound",  {year = -(1 << 31), month = 1, day = 1})
  expectErr("field 'day' is out-of-bound",   {year = 0, month = 1, day = 2^32})
  expectErr("field 'month' is out-of-bound", {year = 0, month = -((1 << 31) + 1), day = 1})
end
print("os-time-bounds: all cases passed")
