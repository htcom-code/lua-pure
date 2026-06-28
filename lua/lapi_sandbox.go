package luapure

import "context"

// Sandboxing and execution control for running untrusted code: a per-call
// _ENV (RunWith), a state with only the safe libraries open (NewSandbox), and
// cooperative cancellation (SetContext).

// SetContext installs ctx as the cancellation context for this state and the
// coroutines it spawns. While set, the VM checks ctx.Err() every finGCPoll
// instructions and raises a catchable error once the context is done, so even
// a tight infinite loop (`while true do end`) can be interrupted. Pass nil to
// disable. Cancellation is observed only between VM instructions, not inside a
// blocking Go call (e.g. a long io read).
func (L *LState) SetContext(ctx context.Context) { L.ctx = ctx }

// RunWith compiles and runs src as a main chunk whose _ENV is env, under a
// protected call, returning all results. Expose only the globals you trust in
// env (build it with NewTable) to confine untrusted code; pair with NewSandbox
// and SetContext for library and time limits.
func (L *LState) RunWith(env *Table, src, name string) ([]Value, error) {
	p, err := CompileString(src, name)
	if err != nil {
		return nil, err
	}
	funcIdx := L.top
	L.push(L.loadProtoEnv(p, mkTable(env)))
	if err := L.pcall(funcIdx, multRet); err != nil {
		return nil, err
	}
	res := make([]Value, L.top-funcIdx)
	copy(res, L.stack[funcIdx:L.top])
	L.top = funcIdx
	return res, nil
}

// NewSandbox returns a state with only the safe standard libraries open: base,
// string, table, math, utf8, and coroutine. The io, os, debug, and package
// libraries are not opened, and the base globals that load or run arbitrary
// code or files (load, loadfile, dofile) are removed. collectgarbage is kept.
// For per-call confinement of a specific chunk, also use RunWith with a custom
// _ENV; for a time limit, SetContext.
func NewSandbox() *LState {
	L := NewState()
	L.OpenBase()
	L.OpenString()
	L.OpenTable()
	L.OpenMath()
	L.OpenUTF8()
	L.OpenCoroutine()
	for _, name := range []string{"load", "loadfile", "dofile"} {
		L.SetGlobal(name, Nil)
	}
	return L
}
