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

// SetRecoverGoPanics toggles protected mode: when on, a protected call (Call,
// DoString, the Lua-level pcall, metamethods) that hits a non-LuaError Go panic
// — typically a registered Go callback that panicked — recovers it into a
// catchable *GoPanicError and unwinds the VM to its pre-call state, so the panic
// does not escape to the host and the State remains reusable. When off (the
// default) such a panic is re-raised unchanged, which is PUC-faithful (PUC does
// not catch a C-side abort) and surfaces genuine host bugs. A pool that runs
// less-trusted callbacks typically turns this on. Inherited by coroutines.
func (L *LState) SetRecoverGoPanics(on bool) { L.recoverGoPanics = on }

// instrLimitMsg is the error raised when an instruction budget is exceeded; the
// host maps it (e.g. to a typed ErrInstructionLimit) by matching this text.
const instrLimitMsg = "instruction limit exceeded"

// execBudget caps executed instructions. count is advanced at the
// finalizer-poll gate, so it is a multiple of finGCPoll (the cap's granularity).
type execBudget struct {
	limit uint64
	count uint64
}

// SetInstructionLimit caps how many bytecode instructions this state (and the
// coroutines it spawns, which share the budget) may execute before the VM
// raises a catchable "instruction limit exceeded" error. It is a host-only
// guard against runaway pure-Lua CPU, orthogonal to SetContext's wall-clock
// cancellation: set ExecTimeout-style deadlines with SetContext and a CPU cap
// here. n == 0 disables it. Setting a limit resets the counter, so call it once
// per run (like SetContext). The cap is enforced at the finalizer-poll gate, so
// its granularity is finGCPoll instructions, not exact.
//
// It is deliberately Go-level only — not exposed to Lua via debug.sethook —
// because a script could otherwise remove its own count hook and defeat the cap.
func (L *LState) SetInstructionLimit(n uint64) {
	if n == 0 {
		L.budget = nil
		return
	}
	L.budget = &execBudget{limit: n}
}

// ClearInstructionLimit removes any instruction cap (and its counter).
func (L *LState) ClearInstructionLimit() { L.budget = nil }

// InstructionCount returns the approximate number of instructions executed
// under the current limit (finGCPoll granularity), or 0 if no limit is set.
func (L *LState) InstructionCount() uint64 {
	if L.budget == nil {
		return 0
	}
	return L.budget.count
}

// RunWith compiles and runs src as a main chunk whose _ENV is env, under a
// protected call, returning all results. Expose only the globals you trust in
// env (build it with NewTable) to confine untrusted code; pair with NewSandbox
// and SetContext for library and time limits.
func (L *LState) RunWith(env *Table, src, name string) ([]Value, error) {
	p, err := CompileString(src, name)
	if err != nil {
		return nil, err
	}
	return L.CallProtoEnv(p, env, multRet)
}

// NewSandbox returns a state with only the safe standard libraries open: base,
// string, table, math, utf8, and coroutine. The io, os, debug, and package
// libraries are not opened, and the base globals that load or run arbitrary
// code or files (load, loadfile, dofile) are removed. collectgarbage is kept.
// For per-call confinement of a specific chunk, also use RunWith with a custom
// _ENV; for a time limit, SetContext.
//
// NewSandbox() is shorthand for NewState(WithSandbox()); use the option form to
// combine it with other options, e.g. NewState(WithSandbox(), WithContext(ctx)).
func NewSandbox() *LState { return NewState(WithSandbox()) }
