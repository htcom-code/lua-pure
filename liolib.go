package luapure

import (
	"bufio"
	"io"
	"os"
	"strings"
	"syscall"
)

// A working subset of the io library (liolib.c): file handles as userdata with
// a metatable of methods (read/write/lines/close/seek/flush), the default
// input/output, and the io.* convenience wrappers.

type luaFile struct {
	f      *os.File
	r      *bufio.Reader
	closed bool
	std    bool // a standard stream: cannot be closed
}

func (L *LState) OpenIO() {
	fileMethods := newTable()
	setFuncs(fileMethods, map[string]GoFunc{
		"read":  fileRead,
		"write": fileWrite,
		"lines": fileLines,
		"close": fileClose,
		"seek":  fileSeek,
		"flush": fileFlush,
	})
	fileMT := newTable()
	fileMT.rawset(MkString("__index"), mkTable(fileMethods))
	fileMT.rawset(MkString("__name"), MkString("FILE*"))
	// __gc and __close both close the handle (liolib.c f_gc); a 5.4 file handle
	// is a to-be-closed value via __close.
	fileMT.rawset(MkString("__gc"), NewGoFunc("__gc", fileGc))
	fileMT.rawset(MkString("__close"), NewGoFunc("__close", fileGc))
	L.registry.rawset(MkString("_IO_FILE_MT"), mkTable(fileMT))

	stdout := L.newFile(os.Stdout, fileMT)
	stderr := L.newFile(os.Stderr, fileMT)
	stdin := L.newFile(os.Stdin, fileMT)
	stdout.userData().data.(*luaFile).std = true
	stderr.userData().data.(*luaFile).std = true
	stdin.userData().data.(*luaFile).std = true

	t := newTable()
	setFuncs(t, map[string]GoFunc{
		"write":  ioWrite,
		"read":   ioRead,
		"open":   ioOpen,
		"lines":  ioLines,
		"close":  ioClose,
		"type":   ioType,
		"input":  ioInput,
		"output": ioOutput,
		"flush":  ioFlush,
	})
	t.rawset(MkString("stdout"), stdout)
	t.rawset(MkString("stderr"), stderr)
	t.rawset(MkString("stdin"), stdin)
	L.registry.rawset(MkString("_IO_OUTPUT"), stdout)
	L.registry.rawset(MkString("_IO_INPUT"), stdin)
	L.registerTable("io", t)
}

func (L *LState) newFile(f *os.File, mt *Table) Value {
	return mkUserData(&userData{data: &luaFile{f: f, r: bufio.NewReader(f)}, meta: mt})
}

func (L *LState) checkFile(n int) *luaFile {
	v := L.Arg(n)
	if v.IsUserData() {
		if lf, ok := v.userData().data.(*luaFile); ok {
			return lf
		}
	}
	L.typeArgError(n, "FILE*")
	return nil
}

// toFile is liolib.c's tofile: a valid handle that is not closed, else
// "attempt to use a closed file".
func (L *LState) toFile(n int) *luaFile {
	lf := L.checkFile(n)
	if lf.closed {
		L.errorf("attempt to use a closed file")
	}
	return lf
}

func (L *LState) defaultOutput() *luaFile {
	v := L.registry.rawgetStr("_IO_OUTPUT")
	lf := v.userData().data.(*luaFile)
	if lf.closed {
		L.errorf("attempt to use a closed file")
	}
	return lf
}

func (L *LState) defaultInput() *luaFile {
	v := L.registry.rawgetStr("_IO_INPUT")
	lf := v.userData().data.(*luaFile)
	if lf.closed {
		L.errorf("attempt to use a closed file")
	}
	return lf
}

// --- io.* default-stream selection ---

// ioStream backs both io.input and io.output, mirroring liolib.c's g_iofile:
// with no argument (or nil) it returns the current default file; with a filename
// it opens the file in the given mode (raising on failure) and installs it; with
// a FILE* it validates and installs that handle. Either way the current default
// is returned.
func ioStream(L *LState, regKey, openMode string) int {
	if L.NArgs() >= 1 && !L.Arg(1).IsNil() {
		arg := L.Arg(1)
		// lua_tostring coerces strings AND numbers to a filename; anything else
		// must be a file handle (g_iofile).
		if arg.IsString() || arg.IsNumber() {
			fname := numToString(arg)
			if arg.IsString() {
				fname = arg.Str()
			}
			flag := os.O_RDONLY
			if openMode == "w" {
				flag = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
			}
			f, err := os.OpenFile(fname, flag, 0644)
			if err != nil {
				L.errorf("cannot open file '%s' (%s)", fname, errReason(err))
			}
			mt := L.registry.rawgetStr("_IO_FILE_MT").tablev()
			L.registry.rawset(MkString(regKey), L.newFile(f, mt))
		} else { // a file handle — tofile() requires an open stream
			lf := L.checkFile(1)
			if lf.closed {
				L.errorf("attempt to use a closed file")
			}
			L.registry.rawset(MkString(regKey), arg)
		}
	}
	L.Push(L.registry.rawgetStr(regKey))
	return 1
}

// errReason extracts the underlying reason from an os error, approximating
// strerror(errno) used by liolib.c's opencheck ("cannot open file '%s' (%s)").
func errReason(err error) string {
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err.Error()
	}
	return err.Error()
}

// errnoOf extracts the numeric errno from an os error for luaL_fileresult's
// third return value, or 0 if unavailable.
func errnoOf(err error) int {
	if pe, ok := err.(*os.PathError); ok {
		if errno, ok := pe.Err.(syscall.Errno); ok {
			return int(errno)
		}
	}
	return 0
}

func ioInput(L *LState) int  { return ioStream(L, "_IO_INPUT", "r") }
func ioOutput(L *LState) int { return ioStream(L, "_IO_OUTPUT", "w") }

// --- io.* wrappers over the default streams ---

func ioWrite(L *LState) int {
	if writeTo(L, L.defaultOutput(), 1) == 0 {
		return 0
	}
	L.Push(L.registry.rawgetStr("_IO_OUTPUT")) // return the file for chaining
	return 1
}

func ioRead(L *LState) int {
	return readFrom(L, L.defaultInput(), 1)
}

func ioOpen(L *LState) int {
	name := L.checkString(1)
	mode := "r"
	if L.NArgs() >= 2 && L.Arg(2).IsString() {
		mode = L.Arg(2).Str()
	}
	if !checkMode(mode) {
		L.argError(2, "invalid mode")
	}
	flag := os.O_RDONLY
	switch strings.TrimSuffix(mode, "b") {
	case "r":
		flag = os.O_RDONLY
	case "w":
		flag = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	case "a":
		flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	case "r+":
		flag = os.O_RDWR
	case "w+":
		flag = os.O_RDWR | os.O_CREATE | os.O_TRUNC
	case "a+":
		flag = os.O_RDWR | os.O_CREATE | os.O_APPEND
	}
	f, err := os.OpenFile(name, flag, 0644)
	if err != nil {
		L.Push(Nil)
		L.Push(MkString(name + ": " + err.Error()))
		L.Push(Int(2))
		return 3
	}
	mt := L.registry.rawgetStr("_IO_FILE_MT").tablev()
	L.Push(L.newFile(f, mt))
	return 1
}

func ioLines(L *LState) int {
	var lf *luaFile
	if L.NArgs() >= 1 && L.Arg(1).IsString() {
		f, err := os.Open(L.Arg(1).Str())
		if err != nil {
			L.errorf("cannot open '%s'", L.Arg(1).Str())
		}
		lf = &luaFile{f: f, r: bufio.NewReader(f)}
	} else {
		lf = L.defaultInput()
	}
	L.Push(linesIterator(L, lf))
	return 1
}

func ioClose(L *LState) int {
	lf := L.defaultOutput()
	if L.NArgs() >= 1 {
		lf = L.toFile(1)
	}
	return doClose(L, lf)
}

func ioType(L *LState) int {
	v := L.Arg(1)
	if v.IsUserData() {
		if lf, ok := v.userData().data.(*luaFile); ok {
			if lf.closed {
				L.Push(MkString("closed file"))
			} else {
				L.Push(MkString("file"))
			}
			return 1
		}
	}
	L.Push(Nil)
	return 1
}

// --- file methods ---

func fileWrite(L *LState) int {
	lf := L.toFile(1)
	if writeTo(L, lf, 2) == 0 {
		return 0
	}
	L.Push(L.Arg(1)) // return the file for chaining
	return 1
}

func fileRead(L *LState) int {
	return readFrom(L, L.toFile(1), 2)
}

func fileClose(L *LState) int {
	return doClose(L, L.toFile(1))
}

func fileFlush(L *LState) int {
	lf := L.toFile(1)
	lf.f.Sync()
	L.Push(L.Arg(1))
	return 1
}

// fileGc backs the file handle's __gc and __close metamethods (liolib.c f_gc):
// it closes an open, non-standard handle. Called bare it still validates its
// argument, so getmetatable(io.stdin).__gc() reports "(FILE* expected, got no
// value)".
func fileGc(L *LState) int {
	lf := L.checkFile(1)
	if !lf.closed && !lf.std && lf.f != nil {
		lf.f.Close()
		lf.closed = true
	}
	return 0
}

// ioFlush flushes the default output stream (liolib.c io_flush).
func ioFlush(L *LState) int {
	lf := L.defaultOutput()
	lf.f.Sync()
	L.Push(True)
	return 1
}

func fileSeek(L *LState) int {
	lf := L.toFile(1)
	whence := "cur"
	if L.NArgs() >= 2 && L.Arg(2).IsString() {
		whence = L.Arg(2).Str()
	}
	offset := L.optInt(3, 0)
	var w int
	switch whence {
	case "set":
		w = io.SeekStart
	case "cur":
		w = io.SeekCurrent
		// The bufio.Reader has read ahead, so the OS fd sits past the logical
		// read position by the buffered byte count; rebase a relative seek onto
		// the logical position so seek()/seek("cur") match C stdio's ftell.
		if lf.r != nil {
			offset -= int64(lf.r.Buffered())
		}
	case "end":
		w = io.SeekEnd
	}
	pos, err := lf.f.Seek(offset, w)
	if err != nil {
		// luaL_fileresult: (fail, strerror, errno)
		L.Push(Nil)
		L.Push(MkString(errReason(err)))
		L.Push(Int(int64(errnoOf(err))))
		return 3
	}
	lf.r = bufio.NewReader(lf.f)
	L.Push(Int(pos))
	return 1
}

func fileLines(L *LState) int {
	lf := L.toFile(1)
	L.Push(linesIterator(L, lf))
	return 1
}

// --- shared read/write helpers ---

func writeTo(L *LState, lf *luaFile, first int) int {
	for i := first; i <= L.NArgs(); i++ {
		v := L.Arg(i)
		if !v.IsString() && !v.IsNumber() {
			L.argError(i, "string expected")
		}
		lf.f.WriteString(tostr(v))
	}
	return 1
}

// readFrom ports g_read: with no format it reads one chopped line; otherwise it
// reads each format in turn and stops at the first that hits end-of-file, whose
// result becomes nil (the remaining formats are not attempted).
func readFrom(L *LState, lf *luaFile, first int) int {
	nargs := L.NArgs()
	if first > nargs {
		if line, ok := readLine(lf, false); ok {
			L.Push(MkString(line))
		} else {
			L.Push(Nil)
		}
		return 1
	}
	results := 0
	success := true
	for i := first; i <= nargs && success; i++ {
		spec := L.Arg(i)
		if spec.IsNumber() {
			n := int(spec.AsInt())
			if n == 0 { // test_eof: "" if more to read, else fail
				L.Push(MkString(""))
				success = testEOF(lf)
			} else {
				s, ok := readChars(lf, n)
				L.Push(MkString(s))
				success = ok
			}
		} else {
			fmtStr := strings.TrimPrefix(spec.Str(), "*")
			if fmtStr == "" {
				L.argError(i, "invalid format")
			}
			switch fmtStr[0] {
			case 'n':
				v, ok := readNumber(lf)
				L.Push(v)
				success = ok
			case 'l':
				line, ok := readLine(lf, false)
				L.Push(MkString(line))
				success = ok
			case 'L':
				line, ok := readLine(lf, true)
				L.Push(MkString(line))
				success = ok
			case 'a':
				L.Push(MkString(readAll(lf))) // always succeeds
			default:
				L.argError(i, "invalid format")
			}
		}
		results++
	}
	if !success {
		L.stack[L.top-1] = Nil // replace the failed read's result with nil
	}
	return results
}

// readChars reads up to n bytes (read_chars); ok is false at immediate EOF.
func readChars(lf *luaFile, n int) (string, bool) {
	buf := make([]byte, n)
	m, _ := io.ReadFull(lf.r, buf)
	return string(buf[:m]), m > 0
}

// readAll reads to end-of-file (read_all).
func readAll(lf *luaFile) string {
	data, _ := io.ReadAll(lf.r)
	return string(data)
}

// testEOF reports whether the stream has more data (test_eof).
func testEOF(lf *luaFile) bool {
	_, err := lf.r.Peek(1)
	return err == nil
}

// readNumber ports read_number: read the longest valid numeral prefix from the
// stream (leaving the look-ahead byte unread) and convert it, like PUC.
func readNumber(lf *luaFile) (Value, bool) {
	const maxlen = 200 // L_MAXLENNUM
	br := lf.r
	var buff []byte
	cur, e := br.ReadByte()
	eof := e != nil
	overflow := false
	nextc := func() bool {
		if len(buff) >= maxlen {
			overflow = true
			return false
		}
		buff = append(buff, cur)
		cur, e = br.ReadByte()
		eof = e != nil
		return true
	}
	test2 := func(a, b byte) bool {
		if !eof && (cur == a || cur == b) {
			return nextc()
		}
		return false
	}
	readdigits := func(hex bool) int {
		cnt := 0
		for !eof {
			ok := cur >= '0' && cur <= '9'
			if hex {
				ok = ok || (cur >= 'a' && cur <= 'f') || (cur >= 'A' && cur <= 'F')
			}
			if !ok || !nextc() {
				break
			}
			cnt++
		}
		return cnt
	}
	for !eof && isSpace(cur) { // skip leading spaces
		cur, e = br.ReadByte()
		eof = e != nil
	}
	count := 0
	hex := false
	test2('-', '+') // optional sign
	if test2('0', '0') {
		if test2('x', 'X') {
			hex = true
		} else {
			count = 1
		}
	}
	count += readdigits(hex)
	if test2('.', '.') { // decimal point
		count += readdigits(hex)
	}
	if count > 0 {
		var mark bool
		if hex {
			mark = test2('p', 'P')
		} else {
			mark = test2('e', 'E')
		}
		if mark {
			test2('-', '+')
			readdigits(false)
		}
	}
	if !eof {
		br.UnreadByte() // unread the look-ahead byte
	}
	if overflow {
		return Nil, false
	}
	if num, ok := str2num(string(buff)); ok {
		return num, true
	}
	return Nil, false
}

// checkMode validates an io.open mode (l_checkmode): r/w/a, optional '+', then
// only the 'b' extension.
func checkMode(mode string) bool {
	if mode == "" || !strings.ContainsRune("rwa", rune(mode[0])) {
		return false
	}
	mode = mode[1:]
	if len(mode) > 0 && mode[0] == '+' {
		mode = mode[1:]
	}
	for i := 0; i < len(mode); i++ {
		if mode[i] != 'b' {
			return false
		}
	}
	return true
}

func readLine(lf *luaFile, keepEOL bool) (string, bool) {
	line, err := lf.r.ReadString('\n')
	if line == "" && err != nil {
		return "", false
	}
	if !keepEOL {
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
	}
	return line, true
}

func doClose(L *LState, lf *luaFile) int {
	if lf.std { // io_noclose: standard streams stay open
		L.Push(Nil)
		L.Push(MkString("cannot close standard file"))
		return 2
	}
	if !lf.closed {
		lf.f.Close()
	}
	lf.closed = true
	L.Push(True)
	return 1
}

func linesIterator(L *LState, lf *luaFile) Value {
	return NewGoFunc("lines_iter", func(L *LState) int {
		line, ok := readLine(lf, false)
		if !ok {
			L.Push(Nil)
			return 1
		}
		L.Push(MkString(line))
		return 1
	})
}
