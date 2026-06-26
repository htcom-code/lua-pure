package luapure


// Public surface for embedding the VM: compiling source, running a chunk, and
// the minimal stack accessors native functions and tests rely on. This is the
// luapure analogue of the lapi.c entry points, kept deliberately small for now.

// loadProto instantiates a compiled prototype into a closure, wiring its first
// upvalue (_ENV) to the globals table the way lua_load does for a main chunk.
func (L *LState) loadProto(p *Proto) Value {
	return L.loadProtoEnv(p, mkTable(L.globals))
}

// loadProtoEnv is loadProto with an explicit _ENV value (for load(..., env)).
func (L *LState) loadProtoEnv(p *Proto, env Value) Value {
	cl := newLuaClosure(p)
	if len(cl.upvals) > 0 {
		cl.upvals[0] = &Upvalue{l: L, idx: -1, v: env}
	}
	for i := range cl.upvals {
		if cl.upvals[i] == nil {
			cl.upvals[i] = &Upvalue{l: L, idx: -1, v: Nil}
		}
	}
	return mkClosure(cl)
}

// CompileString parses and compiles Lua source into a prototype. The chunk name
// is used for error positions and debug info (PUC's "@file" / "=name" forms).
func CompileString(src, name string) (*Proto, error) {
	return compileTokens(src, name)
}

// DoString compiles and runs src as a main chunk, returning its results.
func (L *LState) DoString(src, name string) ([]Value, error) {
	p, err := CompileString(src, name)
	if err != nil {
		return nil, err
	}
	return L.CallProto(p, multRet)
}

// CallProto runs a compiled main-chunk prototype under a protected call,
// returning up to nresults results (multRet for all).
func (L *LState) CallProto(p *Proto, nresults int) ([]Value, error) {
	funcIdx := L.top
	L.push(L.loadProto(p))
	if err := L.pcall(funcIdx, nresults); err != nil {
		return nil, err
	}
	res := make([]Value, L.top-funcIdx)
	copy(res, L.stack[funcIdx:L.top])
	L.top = funcIdx
	return res, nil
}

// --- globals ---

// SetGlobal sets _ENV[name] = v.
func (L *LState) SetGlobal(name string, v Value) {
	L.globals.rawset(MkString(name), v)
}

// GetGlobal returns _ENV[name].
func (L *LState) GetGlobal(name string) Value {
	return L.globals.rawgetStr(name)
}

// Register installs a native function as a global.
func (L *LState) Register(name string, fn GoFunc) {
	L.SetGlobal(name, mkClosure(newGoClosure(fn, name, 0)))
}

// NewGoFunc wraps a GoFunc as a callable Value.
func NewGoFunc(name string, fn GoFunc) Value {
	return mkClosure(newGoClosure(fn, name, 0))
}

// --- native-function stack access ---
//
// Within a GoFunc, arguments occupy stack slots ci.base .. top-1 (1-based via
// Arg); results are produced by Push and the function returns their count.

// NArgs reports how many arguments the current native call received.
func (L *LState) NArgs() int { return L.top - L.ci.base }

// Arg returns the n-th argument (1-based), or Nil if absent.
func (L *LState) Arg(n int) Value {
	idx := L.ci.base + n - 1
	if n >= 1 && idx < L.top {
		return L.stack[idx]
	}
	return Nil
}

// Push appends a value (typically a result) to the stack.
func (L *LState) Push(v Value) { L.push(v) }

// CallValue invokes fn with the given arguments, returning up to nresults
// results. Intended for use from native functions (e.g. pcall, pairs).
func (L *LState) CallValue(fn Value, args []Value, nresults int) []Value {
	funcIdx := L.top
	L.push(fn)
	for _, a := range args {
		L.push(a)
	}
	L.call(funcIdx, nresults)
	res := make([]Value, L.top-funcIdx)
	copy(res, L.stack[funcIdx:L.top])
	L.top = funcIdx
	return res
}
