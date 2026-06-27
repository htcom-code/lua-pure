-- gopher-lua bugfix regression: os.date / strftime conversion specifiers
--
-- Pins os.date() format flags to PUC Lua 5.1's strftime in the C locale.
-- Every `want` was captured from the reference interpreter lua-5.1.5 with
-- LC_ALL=C (`os.date('!<fmt>', ts)`), using UTC (the leading '!').
--
-- Backs the fork fix to cDateFlagToGo (utils.go): upstream #534 fixed %x,
-- and during PUC review %a and %c were found broken too:
--   %x : "15/04/05" (time!)        -> "01/02/06"        (MM/DD/YY)
--   %a : literal "mon" always      -> "Mon"/"Tue"/...   (Go token was lowercase)
--   %c : "02 Jan 06 15:04 MST"     -> asctime "Mon Jan _2 15:04:05 2006"
--
-- Reused as a PUC-conformance probe on the lua54-dev line.

local function eq(got, want, desc)
  assert(got == want,
    string.format("os-date [%s]: got %q, want %q", desc, tostring(got), tostring(want)))
end

-- reference instant: Mon Jan  2 15:04:05 2006 UTC (the Go reference time)
local MON = 1136214245
local DAY = 86400

-- %x : locale date, C locale = MM/DD/YY (was wrongly the time)
eq(os.date("!%x", MON), "01/02/06", "%x date")

-- %a : abbreviated weekday must track the actual day (was always "mon")
eq(os.date("!%a", MON), "Mon", "%a Mon")
eq(os.date("!%a", MON + DAY), "Tue", "%a Tue")
eq(os.date("!%a", MON + 4 * DAY), "Fri", "%a Fri")
eq(os.date("!%a", MON + 5 * DAY), "Sat", "%a Sat")
eq(os.date("!%a", MON + 6 * DAY), "Sun", "%a Sun")

-- %A : full weekday (already correct, kept as guard)
eq(os.date("!%A", MON), "Monday", "%A Monday")
eq(os.date("!%A", MON + DAY), "Tuesday", "%A Tuesday")

-- %c : asctime form, space-padded day
eq(os.date("!%c", MON), "Mon Jan  2 15:04:05 2006", "%c single-digit day")
eq(os.date("!%c", 1152801645), "Thu Jul 13 14:40:45 2006", "%c double-digit day")

-- %X time, %p AM/PM, %I 12-hour (midnight -> 12 AM)
eq(os.date("!%X", MON), "15:04:05", "%X time")
eq(os.date("!%p", MON), "PM", "%p PM")
eq(os.date("!%p", 1136160000), "AM", "%p AM (midnight)")
eq(os.date("!%I:%M", 1136160000), "12:00", "%I midnight = 12")

-- combined string (mirrors upstream #534's test, but with PUC-correct values)
eq(os.date("!weekday=%w|%a|%A, month=%b|%B, year=%y, time=%I:%M|%H:%M:%S|%X, date=%Y-%m-%d|%x", MON),
   "weekday=1|Mon|Monday, month=Jan|January, year=06, time=03:04|15:04:05|15:04:05, date=2006-01-02|01/02/06",
   "combined")

-- expanded strftime flags (PUC-conformant subset of upstream #465; the
-- Ruby-style %-d/%-m modifier and %l are excluded because PUC 5.1 emits
-- them literally / space-padded differently)
local JUL = 1152801645 -- 2006-07-13 14:40:45Z
eq(os.date("!%n", MON), "\n", "%n newline")
eq(os.date("!%t", MON), "\t", "%t tab")
eq(os.date("!%j", MON), "002", "%j day-of-year Jan 2")
eq(os.date("!%j", JUL), "194", "%j day-of-year Jul 13")
eq(os.date("!%e", MON), " 2", "%e space-padded day")
eq(os.date("!%e", JUL), "13", "%e two-digit day")
eq(os.date("!%D", MON), "01/02/06", "%D = MM/DD/YY")
eq(os.date("!%r", MON), "03:04:05 PM", "%r 12-hour PM")
eq(os.date("!%r", 1136160000), "12:00:00 AM", "%r midnight AM")
eq(os.date("!%R", MON), "15:04", "%R HH:MM")
eq(os.date("!%T", MON), "15:04:05", "%T HH:MM:SS")
eq(os.date("!%G", MON), "2006", "%G ISO year")
eq(os.date("!%g", MON), "06", "%g ISO year 2-digit")
eq(os.date("!%V", MON), "01", "%V ISO week (zero-padded)")
eq(os.date("!%V", JUL), "28", "%V ISO week Jul")

-- %U (Sunday-first) and %W (Monday-first) week numbers, incl. the year-start
-- Sunday where they diverge: 2006-01-01 is in U-week 01 but W-week 00.
local NYD = 1136073600 -- 2006-01-01 14:40:00Z, a Sunday
eq(os.date("!%U", MON), "01", "%U week Jan 2")
eq(os.date("!%W", MON), "01", "%W week Jan 2")
eq(os.date("!%U", JUL), "28", "%U week Jul 13")
eq(os.date("!%W", JUL), "28", "%W week Jul 13")
eq(os.date("!%U", NYD), "01", "%U Jan 1 Sunday = week 01")
eq(os.date("!%W", NYD), "00", "%W Jan 1 Sunday = week 00")

print("os-date-format: all cases passed")
