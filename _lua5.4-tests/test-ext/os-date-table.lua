-- gopher-lua bugfix regression: os.date "*t" table and nil/coerced args
--
-- Pins os.date to PUC Lua 5.1 (LC_ALL=C):
--   * the "*t" table's `yday` is the real day-of-year (was hardcoded 0)
--   * a nil format behaves like an absent one (default "%c"), a number is
--     coerced to its string, and a non-string/number raises a type error
-- Backs fork fixes for upstream #244 (yday) and #420 / issue #417 (nil args).
-- Reused as a PUC-conformance probe on the lua54-dev line.

local function eq(got, want, desc)
  assert(got == want,
    string.format("os-date-table [%s]: got %s, want %s", desc, tostring(got), tostring(want)))
end

-- *t table fields (UTC). 1136214245 = 2006-01-02 15:04:05Z
local a = os.date("!*t", 1136214245)
eq(a.year, 2006, "year")
eq(a.month, 1, "month")
eq(a.day, 2, "day")
eq(a.hour, 15, "hour")
eq(a.min, 4, "min")
eq(a.sec, 5, "sec")
eq(a.wday, 2, "wday (Mon=2)")
eq(a.yday, 2, "yday Jan 2")
eq(a.isdst, false, "isdst")

-- yday well into the year: 1152801645 = 2006-07-13 -> day 194
eq(os.date("!*t", 1152801645).yday, 194, "yday Jul 13")

-- nil format == absent format (default "%c")
eq(type(os.date(nil)), "string", "os.date(nil) is a string")
eq(os.date(nil, 1136214245), os.date("%c", 1136214245), "os.date(nil,t) == os.date('%c',t)")

-- number format is coerced to its string ("2" has no %, so it is literal)
eq(os.date(2), "2", "os.date(2) -> '2'")

-- non-string/number raises a type error
local ok, msg = pcall(os.date, {})
eq(ok, false, "os.date({}) errors")
assert(string.find(msg, "string expected, got table", 1, true),
  "os-date-table [error msg]: got " .. tostring(msg))

print("os-date-table: all cases passed")
