package luapure

import (
	"context"
	"fmt"
)

// Options for NewState. luapure keeps PUC's configuration knobs as package
// globals (luaconf.go), set once before any State — PUC bakes them in at
// compile time. The three knobs that are read while a State runs, however, can
// also be overridden per State here, which the package-global model cannot
// express (e.g. giving pooled or multi-tenant States different limits in one
// process). The remaining knobs stay package-global because they are read by
// the stateless compiler, where there is no State to carry them.
//
// NewState() with no options behaves exactly as before: no libraries opened,
// limits taken from the package globals. Each option layers on top.

// stateConfig holds the per-State limit overrides. It is initialized from the
// package globals at NewState and then settled by the options, so an
// unconfigured State mirrors the process-wide defaults.
type stateConfig struct {
	maxStack          int // MaxStack
	maxCCalls         int // MaxCCalls
	maxTableArraySize int // MaxTableArraySize
}

func defaultStateConfig() stateConfig {
	return stateConfig{
		maxStack:          MaxStack,
		maxCCalls:         MaxCCalls,
		maxTableArraySize: MaxTableArraySize,
	}
}

// validate rejects nonsensical limits at construction. PUC leaves these
// unvalidated, but in Go a bad MaxStack would otherwise surface as a panic from
// deep inside DoString (the stack-overflow check fires before the protected
// call), and a bad MaxCCalls as an opaque "C stack overflow" on the first call.
// Failing here, at NewState, points the embedder straight at the bad option.
// maxTableArraySize == 0 is valid (it means "unlimited"); only a negative cap
// is rejected.
func (c stateConfig) validate() {
	if c.maxStack <= 0 {
		panic(fmt.Sprintf("luapure: MaxStack must be > 0, got %d (WithMaxStack or the package global)", c.maxStack))
	}
	if c.maxCCalls <= 0 {
		panic(fmt.Sprintf("luapure: MaxCCalls must be > 0, got %d (WithMaxCCalls or the package global)", c.maxCCalls))
	}
	if c.maxTableArraySize < 0 {
		panic(fmt.Sprintf("luapure: MaxTableArraySize must be >= 0 (0 = unlimited), got %d", c.maxTableArraySize))
	}
}

// libMode selects which libraries NewState opens.
type libMode int

const (
	libsNone    libMode = iota // open nothing (the default; call OpenLibs yourself)
	libsAll                    // OpenLibs
	libsSandbox                // the safe-library set (see NewSandbox)
)

// buildOpts accumulates option effects before NewState applies them.
type buildOpts struct {
	cfg             stateConfig
	libs            libMode
	ctx             context.Context
	hasCtx          bool
	recoverGoPanics bool
}

func newBuildOpts(opts []Option) buildOpts {
	b := buildOpts{cfg: defaultStateConfig()}
	for _, o := range opts {
		o(&b)
	}
	return b
}

// Option configures a State created by NewState.
type Option func(*buildOpts)

// WithOpenLibs opens the full standard library (equivalent to calling
// OpenLibs on the new State).
func WithOpenLibs() Option { return func(b *buildOpts) { b.libs = libsAll } }

// WithSandbox opens only the safe standard libraries, exactly as NewSandbox
// does: base, string, table, math, utf8, and coroutine, with load/loadfile/
// dofile removed. Pair with WithContext and RunWith to confine untrusted code.
func WithSandbox() Option { return func(b *buildOpts) { b.libs = libsSandbox } }

// WithContext installs ctx as the State's cancellation context (equivalent to
// SetContext); the coroutines it spawns inherit it.
func WithContext(ctx context.Context) Option {
	return func(b *buildOpts) { b.ctx = ctx; b.hasCtx = true }
}

// WithRecoverGoPanics turns on protected mode (equivalent to SetRecoverGoPanics):
// a non-LuaError Go panic from a registered callback is recovered into a
// catchable *GoPanicError and the VM is unwound cleanly, instead of escaping to
// the host. Off by default (PUC-faithful re-panic); useful for pools that must
// survive a panicking callback.
func WithRecoverGoPanics() Option { return func(b *buildOpts) { b.recoverGoPanics = true } }

// WithMaxStack overrides MaxStack for this State only: the value-stack ceiling
// that turns unbounded recursion into a catchable "stack overflow".
func WithMaxStack(n int) Option { return func(b *buildOpts) { b.cfg.maxStack = n } }

// WithMaxCCalls overrides MaxCCalls for this State only: the nested Go-call
// depth (metamethods, pcall, hooks, resume) before "C stack overflow".
func WithMaxCCalls(n int) Option { return func(b *buildOpts) { b.cfg.maxCCalls = n } }

// WithMaxTableArraySize overrides MaxTableArraySize for this State only: the
// array-part growth ceiling that stands in for PUC's malloc-failure path. 0
// means unlimited.
func WithMaxTableArraySize(n int) Option {
	return func(b *buildOpts) { b.cfg.maxTableArraySize = n }
}

// openSandboxLibs opens only the safe libraries; the single source of truth for
// both WithSandbox and NewSandbox.
func (L *LState) openSandboxLibs() {
	L.OpenBase()
	L.OpenString()
	L.OpenTable()
	L.OpenMath()
	L.OpenUTF8()
	L.OpenCoroutine()
	for _, name := range []string{"load", "loadfile", "dofile"} {
		L.SetGlobal(name, Nil)
	}
}
