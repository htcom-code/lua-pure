package luapure

import (
	"io"
	"os"
)

// File loading and lifecycle convenience for embedders.

// LoadFile compiles the file at path into a prototype without running it,
// streaming it from disk (text or a precompiled binary chunk, like loadfile).
// A compile or read error is returned; a Lua syntax error comes back as a
// *LuaError.
func (L *LState) LoadFile(path string) (*Proto, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	p, errv, bad := loadDiskFile(f, "@"+path, "bt")
	if bad {
		return nil, &LuaError{value: errv}
	}
	return p, nil
}

// DoFile compiles and runs the file at path as a main chunk under a protected
// call, returning its results (the dofile convenience).
func (L *LState) DoFile(path string) ([]Value, error) {
	p, err := L.LoadFile(path)
	if err != nil {
		return nil, err
	}
	return L.CallProto(p, multRet)
}

// CompileReader compiles Lua source streamed from r into a prototype, the
// io.Reader analogue of CompileString. It accepts either text or a precompiled
// binary chunk and consumes r incrementally rather than buffering it whole.
func CompileReader(r io.Reader, name string) (*Proto, error) {
	p, errv, bad := loadDiskFile(r, name, "bt")
	if bad {
		return nil, &LuaError{value: errv}
	}
	return p, nil
}

// Close releases OS resources the state holds. It closes any non-standard file
// the state opened as its default I/O (e.g. a file passed to io.output), then
// forces a GC cycle and drains pending __gc finalizers so handles no longer
// referenced by the script are closed too. Because lua-pure delegates
// collection to the Go runtime, handles still reachable from the state are
// closed when the state itself becomes unreachable. Close is idempotent; do not
// run the state afterwards.
func (L *LState) Close() {
	for _, key := range []string{"_IO_OUTPUT", "_IO_INPUT"} {
		v := L.registry.rawgetStr(key)
		if !v.IsUserData() {
			continue
		}
		if lf, ok := v.userData().data.(*luaFile); ok && !lf.std && !lf.closed && lf.f != nil {
			lf.f.Close()
			lf.closed = true
		}
	}
	L.finalizeAll()
}
