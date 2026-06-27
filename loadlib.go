package luapure

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// The module system (loadlib.c / lbaselib.c's load): package, require, load,
// loadstring, dofile. Enough of the package machinery to satisfy the test
// suite's `require"x"` headers and `load(code)` usage.

// openPackage creates the package table and installs require. Core libraries
// opened afterwards register themselves in package.loaded via registerTable.
func (L *LState) openPackage() {
	pkg := newTable()
	loaded := newTable()
	preload := newTable()
	pkg.rawset(MkString("loaded"), mkTable(loaded))
	pkg.rawset(MkString("preload"), mkTable(preload))
	pkg.rawset(MkString("path"), MkString("./?.lua;./?/init.lua"))
	pkg.rawset(MkString("cpath"), MkString(""))
	pkg.rawset(MkString("config"), MkString("/\n;\n?\n!\n-\n"))
	setFuncs(pkg, map[string]GoFunc{
		"searchpath": pkgSearchPath,
		"loadlib":    pkgLoadlib,
	})
	// package.searchers: the searcher list ll_require iterates (createsearcherstable).
	searchers := newTable()
	searchers.rawset(Int(1), NewGoFunc("searcher_preload", searcherPreload))
	searchers.rawset(Int(2), NewGoFunc("searcher_Lua", searcherLua))
	searchers.rawset(Int(3), NewGoFunc("searcher_C", searcherC))
	searchers.rawset(Int(4), NewGoFunc("searcher_Croot", searcherCroot))
	pkg.rawset(MkString("searchers"), mkTable(searchers))
	L.pkgLoaded = loaded
	L.pkgTable = pkg
	L.SetGlobal("package", mkTable(pkg))
	loaded.rawset(MkString("package"), mkTable(pkg))
	loaded.rawset(MkString("_G"), mkTable(L.globals))
	L.Register("require", pkgRequire)
}

// pkgLoadlib implements package.loadlib. luapure cannot load shared objects, so
// it behaves exactly like PUC's no-LOADLIB fallback (ll_loadlib + lsys_load):
// it always fails, returning (nil, DLMSG, "absent") — LIB_FAIL is "absent".
func pkgLoadlib(L *LState) int {
	L.checkString(1) // path
	L.checkString(2) // init function name
	L.Push(Nil)
	L.Push(MkString("dynamic libraries not enabled; check your Lua installation")) // DLMSG
	L.Push(MkString("absent"))
	return 3
}

// pkgSearchPath implements package.searchpath(name, path [, sep [, rep]]).
// Faithful to loadlib.c's searchpath/ll_searchpath: replace sep ('.') by dirsep
// in name when present, substitute every '?' (LUA_PATH_MARK) in the whole path
// at once, then try each ';'-separated candidate. On failure return nil plus a
// message built by replacing each ';' with "'\n\tno file '" (pusherrornotfound).
func pkgSearchPath(L *LState) int {
	name := L.checkString(1)
	path := L.checkString(2)
	sep := "."
	if L.NArgs() >= 3 && L.Arg(3).IsString() {
		sep = L.Arg(3).Str()
	}
	dirsep := string(os.PathSeparator) // LUA_DIRSEP
	if L.NArgs() >= 4 && L.Arg(4).IsString() {
		dirsep = L.Arg(4).Str()
	}
	fn, errmsg := searchPathCore(name, path, sep, dirsep)
	if fn != "" {
		L.Push(MkString(fn))
		return 1
	}
	L.Push(Nil)
	L.Push(MkString(errmsg))
	return 2
}

// pathListSep is LUA_PATH_SEP (';'), the package.path template separator.
const pathListSep = ';'

// searchPathCore is loadlib.c's searchpath(): replace sep by dirsep in name
// when present, substitute every '?' (LUA_PATH_MARK) across the whole path at
// once, then return the first readable candidate. On failure the second result
// is the pusherrornotfound message ("no file 'a'\n\tno file 'b'"); an empty
// first result signals not found.
func searchPathCore(name, path, sep, dirsep string) (string, string) {
	if sep != "" && strings.Contains(name, sep) {
		name = strings.ReplaceAll(name, sep, dirsep)
	}
	full := strings.ReplaceAll(path, "?", name)
	for _, fn := range strings.Split(full, string(pathListSep)) {
		if readable(fn) {
			return fn, ""
		}
	}
	return "", "no file '" + strings.ReplaceAll(full, string(pathListSep), "'\n\tno file '") + "'"
}

// readable reports whether the file exists and can be opened for reading,
// matching loadlib.c's readable() (fopen(name, "r")).
func readable(name string) bool {
	f, err := os.Open(name)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// pkgRequire mirrors loadlib.c's ll_require: short-circuit on a truthy
// package.loaded[name], otherwise findLoader, run the loader with (name,
// loaderdata), then settle package.loaded[name] and return (module, loaderdata).
func pkgRequire(L *LState) int {
	name := L.checkString(1)
	loaded := L.pkgLoaded
	if v := loaded.rawgetStr(name); isTruthy(v) { // already loaded
		L.Push(v)
		return 1
	}
	loaderFn, loaderData := L.findLoader(name)
	res := L.CallValue(loaderFn, []Value{MkString(name), loaderData}, 1)
	ret := Nil
	if len(res) > 0 {
		ret = res[0]
	}
	if !ret.IsNil() { // non-nil return → LOADED[name] = it
		loaded.rawset(MkString(name), ret)
	}
	mod := loaded.rawgetStr(name)
	if mod.IsNil() { // module set no value → use true
		mod = True
		loaded.rawset(MkString(name), True)
	}
	L.Push(mod)
	L.Push(loaderData)
	return 2
}

// isTruthy is Lua truthiness: everything but nil and false.
func isTruthy(v Value) bool { return !v.IsNil() && !(v.IsBool() && !v.AsBool()) }

// findLoader mirrors loadlib.c's findloader: iterate package.searchers, call
// each with the module name, and return the first that yields a loader function
// (plus its loader data). Searchers that return a string contribute it to the
// accumulated "not found" message. The package table comes from L.pkgTable
// (PUC's upvalue), so a reassigned global 'package' does not break loading.
func (L *LState) findLoader(name string) (Value, Value) {
	sv := L.pkgTable.rawgetStr("searchers")
	if !sv.IsTable() {
		L.errorf("'package.searchers' must be a table")
	}
	searchers := sv.tablev()
	var msg strings.Builder
	for i := 1; ; i++ {
		s := searchers.rawget(Int(int64(i)))
		if s.IsNil() { // no more searchers
			L.errorf("module '%s' not found:%s", name, msg.String())
		}
		res := L.CallValue(s, []Value{MkString(name)}, 2)
		var r0, r1 Value = Nil, Nil
		if len(res) > 0 {
			r0 = res[0]
		}
		if len(res) > 1 {
			r1 = res[1]
		}
		if r0.IsFunction() { // found a loader
			return r0, r1
		} else if r0.IsString() { // searcher returned an error note
			msg.WriteString("\n\t" + r0.Str())
		}
	}
}

// searchField runs searchPathCore over package[field] (findfile), raising
// "'package.%s' must be a string" when the field is not a string.
func (L *LState) searchField(name, field string) (string, string) {
	pv := L.pkgTable.rawgetStr(field)
	if !pv.IsString() {
		L.errorf("'package.%s' must be a string", field)
	}
	return searchPathCore(name, pv.Str(), ".", string(os.PathSeparator))
}

// --- the standard searchers (createsearcherstable) ---

// searcherPreload looks up package.preload[name].
func searcherPreload(L *LState) int {
	name := L.checkString(1)
	pre := L.pkgTable.rawgetStr("preload")
	if pre.IsTable() {
		if f := pre.tablev().rawgetStr(name); f.IsFunction() {
			L.Push(f)
			L.Push(MkString(":preload:"))
			return 2
		}
	}
	L.Push(MkString("no field package.preload['" + name + "']"))
	return 1
}

// searcherLua searches package.path, compiles the file, and returns the chunk
// plus its filename (searcher_Lua + checkload).
func searcherLua(L *LState) int {
	name := L.checkString(1)
	fname, errmsg := L.searchField(name, "path")
	if fname == "" {
		L.Push(MkString(errmsg))
		return 1
	}
	f, rerr := os.Open(fname)
	if rerr != nil {
		L.errorf("error loading module '%s' from file '%s':\n\t%s", name, fname, errReason(rerr))
	}
	defer f.Close()
	p, errv, bad := L.loadDiskFile(f, "@"+fname, "bt")
	if bad {
		L.errorf("error loading module '%s' from file '%s':\n\t%s", name, fname, errv.Str())
	}
	L.Push(L.loadProto(p))
	L.Push(MkString(fname))
	return 2
}

// searcherC searches package.cpath. luapure cannot load shared objects, so a
// located file is unsupported; otherwise it only contributes a "no file" note.
func searcherC(L *LState) int {
	name := L.checkString(1)
	fname, errmsg := L.searchField(name, "cpath")
	if fname == "" {
		L.Push(MkString(errmsg))
		return 1
	}
	L.errorf("error loading module '%s' from file '%s':\n\tC modules are not supported", name, fname)
	return 0 // unreachable
}

// searcherCroot handles the "sub-module of a C root" case. With no C loading it
// can never succeed; it returns nothing for root names and a note otherwise.
func searcherCroot(L *LState) int {
	name := L.checkString(1)
	dot := strings.IndexByte(name, '.')
	if dot < 0 {
		return 0 // is root; no message
	}
	_, errmsg := L.searchField(name[:dot], "cpath")
	L.Push(MkString(errmsg))
	return 1
}

// --- load / loadstring / dofile (base library) ---

// chunkMode rejects a chunk whose binary/text kind is not allowed by mode (PUC
// checkmode): "b" binary only, "t" text only, "bt" both. ok=false returns the
// error message value.
func chunkMode(binary bool, mode string) (Value, bool) {
	if binary && !strings.Contains(mode, "b") {
		return MkString(fmt.Sprintf("attempt to load a binary chunk (mode is '%s')", mode)), false
	}
	if !binary && !strings.Contains(mode, "t") {
		return MkString(fmt.Sprintf("attempt to load a text chunk (mode is '%s')", mode)), false
	}
	return Nil, true
}

// loadString builds a Proto from a complete source string: a binary chunk
// (string.dump output) is deserialized, text is compiled.
func (L *LState) loadString(src, chunkname, mode string) (*Proto, Value, bool) {
	binary := isBinaryChunk(src)
	if errv, ok := chunkMode(binary, mode); !ok {
		return nil, errv, true
	}
	if binary {
		bp, derr := undump(strings.NewReader(src))
		if derr != nil {
			return nil, MkString(derr.Error()), true
		}
		return bp, Nil, false
	}
	cp, err := CompileString(src, chunkname)
	if err != nil {
		return nil, MkString(err.Error()), true
	}
	return cp, Nil, false
}

// loadReader builds a Proto from a reader function, streaming text to the
// compiler (PUC's ZIO) so an unbounded reader hits a lexer limit instead of
// being accumulated to exhaustion. The first piece is read to detect binary vs
// text; binary chunks (rare via a reader) are accumulated then deserialized.
func (L *LState) loadReader(fn Value, chunkname, mode string) (*Proto, Value, bool) {
	readNext := func() (string, bool) {
		res := L.CallValue(fn, nil, 1)
		if len(res) == 0 || res[0].IsNil() {
			return "", false
		}
		if !res[0].IsString() {
			L.runtimeError("reader function must return a string")
		}
		s := res[0].Str()
		if s == "" {
			return "", false // PUC: a nil/empty piece ends the stream
		}
		return s, true
	}
	first, errv, bad := L.protectReader(func() string { s, _ := readNext(); return s })
	if bad {
		return nil, errv, true
	}
	binary := isBinaryChunk(first)
	if errv, ok := chunkMode(binary, mode); !ok {
		return nil, errv, true
	}
	if binary {
		var sb strings.Builder
		sb.WriteString(first)
		if errv, bad := L.protectReaderInto(readNext, &sb); bad {
			return nil, errv, true
		}
		bp, derr := undump(strings.NewReader(sb.String()))
		if derr != nil {
			return nil, MkString(derr.Error()), true
		}
		return bp, Nil, false
	}
	cp, err := compileZIO(newReaderZIO(first, readNext), chunkname, len(first))
	if err != nil {
		if le, ok := err.(*luaError); ok { // a reader error: forward its value
			return nil, le.value, true
		}
		return nil, MkString(err.Error()), true
	}
	return cp, Nil, false
}

// fileChunkReader returns a getF-style reader that pulls fixed-size blocks from
// r on demand. ok=false at EOF. Each block is a fresh copy, so the single
// scratch buffer is safe to reuse across calls. LoadBufferSize (luaconf.go) is
// the disk read-block size.
func fileChunkReader(r io.Reader) func() (string, bool) {
	buf := make([]byte, LoadBufferSize)
	return func() (string, bool) {
		for {
			n, err := r.Read(buf)
			if n > 0 {
				return string(buf[:n]), true
			}
			if err != nil {
				return "", false
			}
			// n == 0 with no error: retry (permitted by io.Reader).
		}
	}
}

// utf8BOM is the UTF-8 byte-order mark PUC's skipBOM strips before lexing.
const utf8BOM = "\xEF\xBB\xBF"

// skipFileComment mirrors PUC luaL_loadfilex's skipBOM + skipcomment: it strips a
// leading UTF-8 BOM and, when the file then begins with '#', the entire first
// line (a Unix shebang, e.g. "#!/usr/bin/lua"), replacing that line with a single
// newline so every later line keeps its original number. It returns the processed
// leading text plus a reader for the remainder. Binary chunks — which never begin
// with a BOM or '#' — pass through untouched, so the caller's signature check
// still sees the real first byte. Stripping happens only on the file path, not in
// load()/the lexer, exactly as in PUC.
func skipFileComment(first string, readNext func() (string, bool)) (string, func() (string, bool)) {
	buf := first
	fill := func(min int) {
		for len(buf) < min {
			piece, ok := readNext()
			if !ok {
				break
			}
			buf += piece
		}
	}
	fill(len(utf8BOM))
	buf = strings.TrimPrefix(buf, utf8BOM)
	fill(1)
	if len(buf) == 0 || buf[0] != '#' {
		return buf, readNext
	}
	// Skip the shebang line, up to and including its newline (or EOF).
	for {
		if i := strings.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
			break
		}
		piece, ok := readNext()
		if !ok {
			buf = "" // whole file was the comment line
			break
		}
		buf = piece
	}
	// PUC keeps a newline in place of the dropped line so text line numbers stay
	// intact, but drops it for a binary chunk (lf.n = 0 in luaL_loadfilex) so the
	// signature byte remains first and the chunk is recognized as binary.
	fill(1)
	if len(buf) > 0 && buf[0] == luaSignature[0] {
		return buf, readNext
	}
	return "\n" + buf, readNext
}

// loadDiskFile streams a file from disk into a Proto, mirroring PUC
// luaL_loadfilex over getF: text is compiled incrementally through a ZIO, while
// a binary chunk (string.dump output) is read in full then deserialized (undump
// needs the whole blob). mode follows checkmode ("b"/"t"/"bt"). A returned
// (errv, true) reports a load-error value rather than a Proto.
func (L *LState) loadDiskFile(r io.Reader, chunkname, mode string) (*Proto, Value, bool) {
	readNext := fileChunkReader(r)
	first, _ := readNext()
	first, readNext = skipFileComment(first, readNext)
	binary := isBinaryChunk(first)
	if errv, ok := chunkMode(binary, mode); !ok {
		return nil, errv, true
	}
	if binary {
		var sb strings.Builder
		sb.WriteString(first)
		for {
			piece, ok := readNext()
			if !ok {
				break
			}
			sb.WriteString(piece)
		}
		bp, derr := undump(strings.NewReader(sb.String()))
		if derr != nil {
			return nil, MkString(derr.Error()), true
		}
		return bp, Nil, false
	}
	cp, err := compileZIO(newReaderZIO(first, readNext), chunkname, len(first))
	if err != nil {
		return nil, MkString(err.Error()), true
	}
	return cp, Nil, false
}

// protectReader runs read (which may call the Lua reader) catching a raised
// error, restoring the frame, and reporting it as the load failure value.
func (L *LState) protectReader(read func() string) (piece string, errv Value, bad bool) {
	savedTop := L.top
	savedCI := L.ci
	defer func() {
		if r := recover(); r != nil {
			le, ok := r.(*luaError)
			if !ok {
				panic(r)
			}
			L.ci = savedCI
			L.top = savedTop
			errv, bad = le.value, true
		}
	}()
	return read(), Nil, false
}

// protectReaderInto accumulates the remaining reader pieces into sb (binary
// chunk path), catching a reader error.
func (L *LState) protectReaderInto(readNext func() (string, bool), sb *strings.Builder) (errv Value, bad bool) {
	savedTop := L.top
	savedCI := L.ci
	defer func() {
		if r := recover(); r != nil {
			le, ok := r.(*luaError)
			if !ok {
				panic(r)
			}
			L.ci = savedCI
			L.top = savedTop
			errv, bad = le.value, true
		}
	}()
	for {
		s, ok := readNext()
		if !ok {
			return Nil, false
		}
		sb.WriteString(s)
	}
}

func baseLoad(L *LState) int {
	arg1 := L.Arg(1)
	chunkname := "=(load)"
	if arg1.IsString() {
		chunkname = arg1.Str()
	} else if !arg1.IsFunction() {
		L.typeArgError(1, "string")
	}
	if L.NArgs() >= 2 && L.Arg(2).IsString() {
		chunkname = L.Arg(2).Str()
	}
	// mode restricts which chunk kinds are accepted (PUC checkmode).
	mode := "bt"
	if L.NArgs() >= 3 && L.Arg(3).IsString() {
		mode = L.Arg(3).Str()
	}

	var (
		p    *Proto
		errv Value
		bad  bool
	)
	if arg1.IsString() {
		p, errv, bad = L.loadString(arg1.Str(), chunkname, mode)
	} else {
		p, errv, bad = L.loadReader(arg1, chunkname, mode)
	}
	if bad {
		L.Push(Nil)
		L.Push(errv)
		return 2
	}
	// PUC lua_load sets the 1st upvalue to the globals table; luaB_load then
	// overrides it with the env argument when one was passed — even if nil. So a
	// present env arg is used as-is; an absent one defaults to the globals.
	env := mkTable(L.globals)
	if L.NArgs() >= 4 {
		env = L.Arg(4)
	}
	L.Push(L.loadProtoEnv(p, env))
	return 1
}

func baseDofile(L *LState) int {
	fname := L.checkString(1)
	f, err := os.Open(fname)
	if err != nil {
		L.errorf("cannot open %s", fname)
	}
	defer f.Close()
	p, errv, bad := L.loadDiskFile(f, "@"+fname, "bt")
	if bad {
		L.throw(errv)
	}
	fnv := L.loadProto(p)
	res := L.CallValue(fnv, nil, multRet)
	for _, v := range res {
		L.Push(v)
	}
	return len(res)
}
