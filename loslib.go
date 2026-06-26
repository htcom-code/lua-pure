package luapure

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// A minimal os library (loslib.c): the time/clock/env/exit pieces the test
// suite touches early. Locale-dependent formatting is approximated.

func (L *LState) OpenOS() {
	t := newTable()
	setFuncs(t, map[string]GoFunc{
		"time":      osTime,
		"clock":     osClock,
		"date":      osDate,
		"difftime":  osDifftime,
		"getenv":    osGetenv,
		"execute":   osExecute,
		"exit":      osExit,
		"setlocale": osSetlocale,
		"tmpname":   osTmpname,
		"remove":    osRemove,
		"rename":    osRename,
	})
	L.registerTable("os", t)
}

var processStart = time.Now()

// osGetfield reads an integer date-table field (getfield in loslib.c): a
// non-integer value errors, an absent required field (def < 0) errors, and an
// absent optional field falls back to its default.
func osGetfield(L *LState, tb *Table, key string, def int) int {
	v := tb.rawgetStr(key)
	if i, ok := tointegerCvt(v); ok {
		return int(i)
	}
	if !v.IsNil() {
		L.errorf("field '%s' is not an integer", key)
	}
	if def < 0 {
		L.errorf("field '%s' missing in date table", key)
	}
	return def
}

func osTime(L *LState) int {
	if L.NArgs() < 1 || L.Arg(1).IsNil() {
		L.Push(Int(time.Now().Unix()))
		return 1
	}
	tb := L.checkTable(1)
	// year/month/day are required (no default); the rest default as in PUC.
	tm := time.Date(
		osGetfield(L, tb, "year", -1), time.Month(osGetfield(L, tb, "month", -1)),
		osGetfield(L, tb, "day", -1), osGetfield(L, tb, "hour", 12),
		osGetfield(L, tb, "min", 0), osGetfield(L, tb, "sec", 0), 0, time.Local)
	osSetallfields(tb, tm) // normalize fields back into the table
	L.Push(Int(tm.Unix()))
	return 1
}

// osSetallfields writes the normalized broken-down time back into a date table
// (setallfields in loslib.c).
func osSetallfields(tb *Table, t time.Time) {
	tb.rawset(MkString("year"), Int(int64(t.Year())))
	tb.rawset(MkString("month"), Int(int64(t.Month())))
	tb.rawset(MkString("day"), Int(int64(t.Day())))
	tb.rawset(MkString("hour"), Int(int64(t.Hour())))
	tb.rawset(MkString("min"), Int(int64(t.Minute())))
	tb.rawset(MkString("sec"), Int(int64(t.Second())))
	tb.rawset(MkString("yday"), Int(int64(t.YearDay())))
	tb.rawset(MkString("wday"), Int(int64(t.Weekday())+1))
	tb.rawset(MkString("isdst"), Bool(t.IsDST()))
}

func osClock(L *LState) int {
	L.Push(Float(time.Since(processStart).Seconds()))
	return 1
}

func osDate(L *LState) int {
	format := "%c"
	if L.NArgs() >= 1 && L.Arg(1).IsString() {
		format = L.Arg(1).Str()
	}
	now := time.Now()
	if L.NArgs() >= 2 {
		if i, ok := tointegerCvt(L.Arg(2)); ok {
			now = time.Unix(i, 0)
		}
	}
	utc := false
	if len(format) > 0 && format[0] == '!' {
		utc = true
		format = format[1:]
		now = now.UTC()
	}
	_ = utc
	if format == "*t" {
		tb := newTable()
		osSetallfields(tb, now)
		L.Push(mkTable(tb))
		return 1
	}
	L.Push(MkString(L.strftime(format, now)))
	return 1
}

// strftimeOpts lists valid strftime specifiers (LUA_STRFTIMEOPTIONS, C99): the
// single-character group, then "||" and the two-character %E/%O groups.
const strftimeOpts = "aAbBcCdDeFgGhHIjmMnprRStTuUVwWxXyYzZ%||EcECExEXEyEYOdOeOHOIOmOMOSOuOUOVOwOWOy"

// osCheckoption validates a conversion specifier at the start of conv against
// strftimeOpts and returns its length (1 or 2), erroring like PUC checkoption.
func (L *LState) osCheckoption(conv string) int {
	opt := strftimeOpts
	oplen := 1
	for o := 0; o < len(opt) && oplen <= len(conv); {
		if opt[o] == '|' { // advance to the next, longer block
			oplen++
			o += oplen
		} else if conv[:oplen] == opt[o:o+oplen] {
			return oplen
		} else {
			o += oplen
		}
	}
	L.argError(1, "invalid conversion specifier '%"+conv+"'")
	return 0
}

// strftime renders the common C strftime specifiers used by os.date, validating
// every specifier through osCheckoption so an invalid one errors as in PUC.
func (L *LState) strftime(format string, t time.Time) string {
	repl := map[string]string{
		"Y": t.Format("2006"), "y": t.Format("06"), "m": t.Format("01"),
		"d": t.Format("02"), "e": t.Format("_2"), "H": t.Format("15"),
		"I": t.Format("03"), "M": t.Format("04"), "S": t.Format("05"),
		"p": t.Format("PM"), "A": t.Format("Monday"), "a": t.Format("Mon"),
		"B": t.Format("January"), "b": t.Format("Jan"), "h": t.Format("Jan"),
		"c": t.Format("Mon Jan  2 15:04:05 2006"), "x": t.Format("01/02/06"),
		"X": t.Format("15:04:05"), "D": t.Format("01/02/06"),
		"F": t.Format("2006-01-02"), "T": t.Format("15:04:05"),
		"R": t.Format("15:04"), "r": t.Format("03:04:05 PM"),
		"z": t.Format("-0700"), "Z": t.Format("MST"), "n": "\n", "t": "\t",
		"%": "%",
	}
	var sb []byte
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			sb = append(sb, format[i])
			continue
		}
		oplen := L.osCheckoption(format[i+1:]) // validates; errors if invalid
		spec := format[i+1 : i+1+oplen]
		i += oplen
		if r, ok := repl[spec]; ok {
			sb = append(sb, r...)
		}
		// Valid-but-unrendered specifiers (e.g. %E*/%O* modifiers) contribute
		// nothing rather than corrupt the output.
	}
	return string(sb)
}

// osExecute runs a shell command (os_execute / luaL_execresult). With no
// command it reports whether a shell is available.
func osExecute(L *LState) int {
	if L.NArgs() < 1 || L.Arg(1).IsNil() {
		L.Push(Bool(true)) // a shell is always available
		return 1
	}
	cmd := L.checkString(1)
	c := exec.Command("/bin/sh", "-c", cmd)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return L.osExecResult(c.Run())
}

// osExecResult mirrors luaL_execresult: (true,"exit",0) on success, (nil,"exit",
// code) on a non-zero exit, and (nil,"signal",n) when killed by a signal.
func (L *LState) osExecResult(err error) int {
	if err == nil {
		L.Push(True)
		L.Push(MkString("exit"))
		L.Push(Int(0))
		return 3
	}
	if ee, ok := err.(*exec.ExitError); ok {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			L.Push(Nil)
			L.Push(MkString("signal"))
			L.Push(Int(int64(ws.Signal())))
			return 3
		}
		L.Push(Nil)
		L.Push(MkString("exit"))
		L.Push(Int(int64(ee.ExitCode())))
		return 3
	}
	L.Push(Nil)
	L.Push(MkString("exit"))
	L.Push(Int(-1))
	return 3
}

// osSetlocale validates the category and reports the locale. Go has no
// setlocale, so only the portable "C"/POSIX locale is honoured.
func osSetlocale(L *LState) int {
	cats := []string{"all", "collate", "ctype", "monetary", "numeric", "time"}
	var l string
	hasL := L.NArgs() >= 1 && !L.Arg(1).IsNil()
	if hasL {
		l = L.checkString(1)
	}
	L.checkOption(2, "all", cats) // errors on an invalid category
	if !hasL || l == "" || l == "C" || l == "POSIX" {
		L.Push(MkString("C"))
	} else {
		L.Push(Nil)
	}
	return 1
}

func osDifftime(L *LState) int {
	L.Push(Float(L.checkNumber(1) - L.checkNumber(2)))
	return 1
}

func osGetenv(L *LState) int {
	if v, ok := os.LookupEnv(L.checkString(1)); ok {
		L.Push(MkString(v))
	} else {
		L.Push(Nil)
	}
	return 1
}

// osExit is a no-op here: a script must not terminate the host process. (The
// suite's drivers that genuinely call os.exit are skipped by the runner.)
func osExit(L *LState) int {
	return 0
}

func osTmpname(L *LState) int {
	f, err := os.CreateTemp("", "luapure_")
	if err != nil {
		L.errorf("unable to generate a unique filename")
	}
	name := f.Name()
	f.Close()
	L.Push(MkString(name))
	return 1
}

func osRemove(L *LState) int {
	if err := os.Remove(L.checkString(1)); err != nil {
		L.Push(Nil)
		L.Push(MkString(err.Error()))
		return 2
	}
	L.Push(True)
	return 1
}

func osRename(L *LState) int {
	if err := os.Rename(L.checkString(1), L.checkString(2)); err != nil {
		L.Push(Nil)
		L.Push(MkString(err.Error()))
		return 2
	}
	L.Push(True)
	return 1
}
