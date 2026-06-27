package luapure

import (
	"bufio"
	"fmt"
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
	w      *bufio.Writer // write buffer when setvbuf selected "full"/"line"; nil = unbuffered
	vbuf   byte          // buffering mode: 'f' full, 'l' line, 'n'/0 none (setvbuf)
	closed bool
	std    bool  // a standard stream: cannot be closed
	ferr   error // last real (non-EOF) read error, PUC's ferror(f) flag
}

// defaultBufSize is the buffer size setvbuf uses when the caller omits one
// (PUC's BUFSIZ-equivalent default).
const defaultBufSize = 4096

// writeString writes s honouring the file's buffering mode: unbuffered files
// write straight through, a "full" buffer accumulates until flush/overflow, and
// a "line" buffer flushes as soon as a newline is written.
func (lf *luaFile) writeString(s string) error {
	if lf.w == nil {
		_, err := lf.f.WriteString(s)
		return err
	}
	if _, err := lf.w.WriteString(s); err != nil {
		return err
	}
	if lf.vbuf == 'l' && strings.IndexByte(s, '\n') >= 0 {
		return lf.w.Flush()
	}
	return nil
}

// flushWrite flushes any buffered output to the underlying file (a no-op for an
// unbuffered handle).
func (lf *luaFile) flushWrite() error {
	if lf.w != nil {
		return lf.w.Flush()
	}
	return nil
}

func (L *LState) OpenIO() {
	fileMethods := newTable()
	setFuncs(fileMethods, map[string]GoFunc{
		"read":  fileRead,
		"write": fileWrite,
		"lines": fileLines,
		"close": fileClose,
		"seek":    fileSeek,
		"flush":   fileFlush,
		"setvbuf": fileSetvbuf,
	})
	fileMT := newTable()
	fileMT.rawset(MkString("__index"), mkTable(fileMethods))
	fileMT.rawset(MkString("__name"), MkString("FILE*"))
	// __tostring renders "file (closed)" or "file (0x...)" (liolib.c f_tostring),
	// overriding the __name fallback ("FILE*: 0x...").
	fileMT.rawset(MkString("__tostring"), NewGoFunc("__tostring", fileToString))
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
		"write":   ioWrite,
		"read":    ioRead,
		"open":    ioOpen,
		"tmpfile": ioTmpfile,
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
		L.errorf("default output file is closed") // getiofile
	}
	return lf
}

func (L *LState) defaultInput() *luaFile {
	v := L.registry.rawgetStr("_IO_INPUT")
	lf := v.userData().data.(*luaFile)
	if lf.closed {
		L.errorf("default input file is closed") // getiofile
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
	if n, ok := writeTo(L, L.defaultOutput(), 1); !ok {
		return n // (nil, message, errno) on write error
	}
	L.Push(L.registry.rawgetStr("_IO_OUTPUT")) // return the file for chaining
	return 1
}

func ioRead(L *LState) int {
	return readFrom(L, L.defaultInput(), 1)
}

// ioTmpfile implements io.tmpfile (liolib.c io_tmpfile / C tmpfile): an
// anonymous read/write file removed automatically. We create a temp file and
// unlink it immediately — on Unix the open fd keeps it alive until close, after
// which the OS reclaims it, matching tmpfile()'s "removed when closed" contract.
func ioTmpfile(L *LState) int {
	f, err := os.CreateTemp("", "luapure")
	if err != nil {
		return pushFileError(L, err)
	}
	os.Remove(f.Name())
	mt := L.registry.rawgetStr("_IO_FILE_MT").tablev()
	L.Push(L.newFile(f, mt))
	return 1
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
	var fv Value
	closeOnEOF := false
	if L.NArgs() >= 1 && L.Arg(1).IsString() {
		f, err := os.Open(L.Arg(1).Str())
		if err != nil {
			L.errorf("cannot open '%s'", L.Arg(1).Str())
		}
		// A file opened by io.lines(filename) is owned by the iterator: wrap it
		// as a real handle (with __close) so it can also serve as the loop's
		// to-be-closed value, and close it at EOF.
		mt := L.registry.rawgetStr("_IO_FILE_MT").tablev()
		fv = L.newFile(f, mt)
		lf = fv.userData().data.(*luaFile)
		closeOnEOF = true
	} else {
		lf = L.defaultInput()
	}
	fmts := collectLineFormats(L, 2)
	L.Push(linesIterator(L, lf, closeOnEOF, fmts))
	if closeOnEOF {
		// Generic-for to-be-closed protocol (io_lines): iterator, state (nil),
		// control (nil), and the file as the to-be-closed value.
		L.Push(Nil)
		L.Push(Nil)
		L.Push(fv)
		return 4
	}
	return 1
}

func ioClose(L *LState) int {
	// Only fall back to the default output when given no argument (io_close);
	// fetching it eagerly would raise on an already-closed default output even
	// when an explicit file was passed.
	var lf *luaFile
	if L.NArgs() >= 1 {
		lf = L.toFile(1)
	} else {
		lf = L.defaultOutput()
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
	if n, ok := writeTo(L, lf, 2); !ok {
		return n // (nil, message, errno) on write error
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
	if err := lf.flushWrite(); err != nil {
		return pushFileError(L, err)
	}
	lf.f.Sync()
	L.Push(L.Arg(1))
	return 1
}

// fileToString backs the file handle's __tostring (liolib.c f_tostring): a
// closed handle renders "file (closed)", an open one "file (0x...)". PUC formats
// the FILE* pointer with %p; we use the underlying *os.File for the same shape.
func fileToString(L *LState) int {
	lf := L.checkFile(1)
	if lf.closed {
		L.Push(MkString("file (closed)"))
	} else {
		L.Push(MkString(fmt.Sprintf("file (%p)", lf.f)))
	}
	return 1
}

// fileGc backs the file handle's __gc and __close metamethods (liolib.c f_gc):
// it closes an open, non-standard handle. Called bare it still validates its
// argument, so getmetatable(io.stdin).__gc() reports "(FILE* expected, got no
// value)".
func fileGc(L *LState) int {
	lf := L.checkFile(1)
	if !lf.closed && !lf.std && lf.f != nil {
		lf.flushWrite()
		lf.f.Close()
		lf.closed = true
	}
	return 0
}

// ioFlush flushes the default output stream (liolib.c io_flush).
func ioFlush(L *LState) int {
	lf := L.defaultOutput()
	if err := lf.flushWrite(); err != nil {
		return pushFileError(L, err)
	}
	lf.f.Sync()
	L.Push(True)
	return 1
}

// fileSetvbuf backs file:setvbuf(mode [, size]) (liolib.c f_setvbuf). It selects
// the handle's output buffering: "no" writes through immediately, "full" holds
// output until the buffer fills or is flushed, "line" flushes on each newline.
// Any output buffered under the previous mode is flushed first. Returns true on
// success (luaL_fileresult).
func fileSetvbuf(L *LState) int {
	lf := L.toFile(1)
	mode := L.checkString(2)
	size := int(L.optInt(3, defaultBufSize))
	if size <= 0 {
		size = defaultBufSize
	}
	if err := lf.flushWrite(); err != nil {
		return pushFileError(L, err)
	}
	switch mode {
	case "no":
		lf.w = nil
		lf.vbuf = 'n'
	case "full":
		lf.w = bufio.NewWriterSize(lf.f, size)
		lf.vbuf = 'f'
	case "line":
		lf.w = bufio.NewWriterSize(lf.f, size)
		lf.vbuf = 'l'
	default:
		L.argError(2, "invalid option '"+mode+"'")
	}
	L.Push(True)
	return 1
}

func fileSeek(L *LState) int {
	lf := L.toFile(1)
	lf.flushWrite() // C fseek flushes pending output before repositioning
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
	// file:lines() does not own/close the file; formats follow the file at arg 1.
	L.Push(linesIterator(L, lf, false, collectLineFormats(L, 2)))
	return 1
}

// --- shared read/write helpers ---

// writeTo writes args first..NArgs to lf (g_write). On success it pushes
// nothing and returns ok=true, leaving the caller to push the file for
// chaining. On a write error (e.g. writing to a read-only handle) it pushes the
// luaL_fileresult failure tuple (nil, message, errno) and returns n=3, ok=false
// so the caller returns those results instead.
func writeTo(L *LState, lf *luaFile, first int) (n int, ok bool) {
	for i := first; i <= L.NArgs(); i++ {
		v := L.Arg(i)
		if !v.IsString() && !v.IsNumber() {
			L.argError(i, "string expected")
		}
		if err := lf.writeString(tostr(v)); err != nil {
			pushFileError(L, err)
			return 3, false
		}
	}
	return 0, true
}

// pushFileError pushes the luaL_fileresult failure tuple (nil, message, errno)
// and returns 3, the number of results.
func pushFileError(L *LState, err error) int {
	L.Push(Nil)
	L.Push(MkString(errReason(err)))
	L.Push(Int(int64(errnoOf(err))))
	return 3
}

// isReadErr reports whether err from a read is a genuine I/O failure rather than
// an ordinary end-of-file (PUC distinguishes feof from ferror).
func isReadErr(err error) bool {
	return err != nil && err != io.EOF && err != io.ErrUnexpectedEOF
}

// readFrom ports g_read: with no format it reads one chopped line; otherwise it
// reads each format in turn and stops at the first that hits end-of-file, whose
// result becomes nil (the remaining formats are not attempted). The format
// specs are L.Arg(first..NArgs).
func readFrom(L *LState, lf *luaFile, first int) int {
	lf.ferr = nil
	nargs := L.NArgs()
	base := L.top
	if first > nargs {
		if line, ok := readLine(lf, false); ok {
			L.Push(MkString(line))
		} else {
			L.Push(Nil)
		}
	} else {
		specs := make([]Value, 0, nargs-first+1)
		for i := first; i <= nargs; i++ {
			specs = append(specs, L.Arg(i))
		}
		readSpecs(L, lf, specs, func(i int) { L.argError(first+i, "invalid format") })
	}
	// A genuine read error (e.g. reading a write-only handle) takes precedence
	// over the nil result, returning luaL_fileresult (nil, message, errno).
	if lf.ferr != nil {
		L.top = base
		return pushFileError(L, lf.ferr)
	}
	return L.top - base
}

// readSpecs is the body of g_read: it applies each read spec against lf, pushing
// one result per spec and returning the count. A numeric spec reads that many
// bytes (0 = EOF test), a string spec selects n/l/L/a. At the first spec that
// hits end-of-file the remaining specs are skipped and the last result is
// nil'd. badSpec(i) reports an invalid spec (i is a 0-based index into specs).
func readSpecs(L *LState, lf *luaFile, specs []Value, badSpec func(i int)) int {
	results := 0
	success := true
	for i := 0; i < len(specs) && success; i++ {
		spec := specs[i]
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
				badSpec(i)
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
				badSpec(i)
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
	m, err := io.ReadFull(lf.r, buf)
	if m == 0 && isReadErr(err) {
		lf.ferr = err
	}
	return string(buf[:m]), m > 0
}

// readAll reads to end-of-file (read_all). io.ReadAll treats EOF as success, so
// any returned error is a genuine read failure.
func readAll(lf *luaFile) string {
	data, err := io.ReadAll(lf.r)
	if err != nil {
		lf.ferr = err
	}
	return string(data)
}

// testEOF reports whether the stream has more data (test_eof).
func testEOF(lf *luaFile) bool {
	_, err := lf.r.Peek(1)
	if isReadErr(err) {
		lf.ferr = err
	}
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
	if isReadErr(e) {
		lf.ferr = e
	}
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
		if isReadErr(err) {
			lf.ferr = err
		}
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
		lf.flushWrite()
		lf.f.Close()
	}
	lf.closed = true
	L.Push(True)
	return 1
}

// maxArgLine bounds the read formats an io.lines/file:lines iterator may carry
// (PUC MAXARGLINE); more raises "too many arguments".
const maxArgLine = 250

// linesIterator builds the io.lines/file:lines iterator (liolib.c io_readline).
// fmts are the read formats captured at the lines() call (empty = one chopped
// line per step). closeOnEOF is true only for the io.lines(filename) form,
// which owns the file it opened and closes it on reaching end-of-file. Either
// way, calling the iterator after the file is closed raises "file is already
// closed".
func linesIterator(L *LState, lf *luaFile, closeOnEOF bool, fmts []Value) Value {
	return NewGoFunc("lines_iter", func(L *LState) int {
		if lf.closed {
			L.errorf("file is already closed")
		}
		lf.ferr = nil
		base := L.top
		var n int
		if len(fmts) == 0 {
			line, ok := readLine(lf, false)
			if ok {
				L.Push(MkString(line))
				return 1
			}
			L.Push(Nil)
			n = 1
		} else {
			n = readSpecs(L, lf, fmts, func(int) { L.errorf("invalid format") })
		}
		// A genuine read error raises here (PUC io_readline turns g_read's
		// fileresult message into an error); a plain EOF ends iteration below.
		if lf.ferr != nil {
			L.errorf("%s", errReason(lf.ferr))
		}
		// EOF when the first result is nil: like PUC io_readline, drop the
		// results and close the file if this iterator owns it.
		if n > 0 && L.stack[base].IsNil() {
			if closeOnEOF && !lf.std && lf.f != nil {
				lf.f.Close()
				lf.closed = true
			}
			L.top = base
			L.Push(Nil)
			return 1
		}
		return n
	})
}

// collectLineFormats gathers the read formats passed to a lines() call,
// L.Arg(first..NArgs), enforcing PUC's MAXARGLINE ceiling.
func collectLineFormats(L *LState, first int) []Value {
	nargs := L.NArgs()
	if nargs-first+1 > maxArgLine {
		L.argError(first+maxArgLine, "too many arguments")
	}
	var fmts []Value
	for i := first; i <= nargs; i++ {
		fmts = append(fmts, L.Arg(i))
	}
	return fmts
}
