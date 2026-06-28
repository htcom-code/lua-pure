package luapure

import (
	"sync"
	"sync/atomic"
)

// Debugger is a breakpoint/step controller built on the Go debug hook
// (SetGoHook). It is the substrate a front end — a DAP server, a CLI console —
// drives: set breakpoints, run, and on each stop inspect frames and choose how
// to resume.
//
// Control flow and goroutines. The hook fires on the goroutine running the VM.
// When a stop condition is met (a breakpoint, a completed step, or an async
// Pause) the Debugger invokes the OnStop handler *synchronously on that
// goroutine* and blocks the VM there until the handler returns. Inside the
// handler the program is frozen, so frames (Frames / the LState's Frame) can be
// read safely; the handler chooses the next action with Continue / StepInto /
// StepOver / StepOut and returns to resume. A front end running on another
// goroutine (reading a socket) coordinates by having the handler block on a
// channel it feeds — the action is still issued on the VM goroutine, which
// keeps frame access race-free.
//
// Methods that configure the session (SetBreakpoints, AddBreakpoint, Pause) are
// safe to call from another goroutine while the VM runs. The resume actions and
// frame inspection are meant to be used from within the OnStop handler.
type Debugger struct {
	L *LState

	mu  sync.Mutex
	bps map[string]map[int]bool // source -> line set

	onStop func(d *Debugger, reason StopReason)

	// Stepping state, touched only on the VM goroutine (hook + applyAction).
	mode        stepMode
	targetDepth int
	action      stepMode // chosen by a resume action during OnStop

	pauseReq int32 // atomic: an async Pause is pending
}

// StopReason explains why the debugger stopped.
type StopReason uint8

const (
	StopBreakpoint StopReason = iota // a set breakpoint was hit
	StopStep                         // a step (into/over/out) completed
	StopPause                        // an async Pause request landed
)

func (r StopReason) String() string {
	switch r {
	case StopBreakpoint:
		return "breakpoint"
	case StopStep:
		return "step"
	case StopPause:
		return "pause"
	}
	return "?"
}

type stepMode uint8

const (
	modeRun      stepMode = iota // run freely; stop only at breakpoints/pause
	modeStepInto                 // stop at the next line, any frame
	modeStepOver                 // stop at the next line at this depth or shallower
	modeStepOut                  // stop at the next line shallower than this depth
)

// NewDebugger attaches a debugger to L and installs its line hook. The session
// starts in run mode (stops only at breakpoints or Pause). Call Detach to
// remove the hook.
func NewDebugger(L *LState) *Debugger {
	d := &Debugger{L: L, bps: make(map[string]map[int]bool), mode: modeRun}
	L.SetGoHook(d.hook, MaskLine, 0)
	return d
}

// OnStop sets the handler invoked (on the VM goroutine, blocking) each time the
// debugger stops. The handler inspects state and calls one resume action.
func (d *Debugger) OnStop(fn func(d *Debugger, reason StopReason)) { d.onStop = fn }

// Detach removes the debug hook; the program then runs unobserved.
func (d *Debugger) Detach() { d.L.ClearGoHook() }

// --- breakpoints (safe to call concurrently with a running VM) ---

// SetBreakpoints replaces the breakpoint set for source with lines. A breakpoint
// matches a frame whose raw chunk name or short name equals source, so "@f.lua",
// "f.lua" and a "=name" chunk are all addressable.
func (d *Debugger) SetBreakpoints(source string, lines []int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(lines) == 0 {
		delete(d.bps, source)
		return
	}
	set := make(map[int]bool, len(lines))
	for _, ln := range lines {
		set[ln] = true
	}
	d.bps[source] = set
}

// AddBreakpoint adds a single source:line breakpoint.
func (d *Debugger) AddBreakpoint(source string, line int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	set := d.bps[source]
	if set == nil {
		set = make(map[int]bool)
		d.bps[source] = set
	}
	set[line] = true
}

// RemoveBreakpoint removes a single source:line breakpoint.
func (d *Debugger) RemoveBreakpoint(source string, line int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if set := d.bps[source]; set != nil {
		delete(set, line)
		if len(set) == 0 {
			delete(d.bps, source)
		}
	}
}

// ClearBreakpoints removes every breakpoint.
func (d *Debugger) ClearBreakpoints() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.bps = make(map[string]map[int]bool)
}

// Pause requests that the debugger stop at the next line it executes. It is
// asynchronous: call it from another goroutine while the VM runs.
func (d *Debugger) Pause() { atomic.StoreInt32(&d.pauseReq, 1) }

// --- resume actions (use from within the OnStop handler) ---

// Continue resumes free execution until the next breakpoint or Pause.
func (d *Debugger) Continue() { d.action = modeRun }

// StepInto resumes until the next source line in any frame (descending into
// calls).
func (d *Debugger) StepInto() { d.action = modeStepInto }

// StepOver resumes until the next source line at the current depth or shallower
// (calls made from this line run to completion without stopping).
func (d *Debugger) StepOver() { d.action = modeStepOver }

// StepOut resumes until execution returns to a shallower frame than the current
// one.
func (d *Debugger) StepOut() { d.action = modeStepOut }

// --- inspection (use from within the OnStop handler) ---

// Frames returns the live call frames, innermost first (level 0 .. depth-1).
// The returned frames are valid only until the handler resumes.
func (d *Debugger) Frames() []Frame {
	var fs []Frame
	for i := 0; ; i++ {
		f, ok := d.L.Frame(i)
		if !ok {
			break
		}
		fs = append(fs, f)
	}
	return fs
}

// --- hook ---

func (d *Debugger) hook(L *LState, ev HookEvent, line int) {
	if ev != HookLine {
		return
	}
	reason, stop := d.shouldStop(L, line)
	if !stop {
		return
	}
	// Default to continue; the handler may pick a step instead.
	d.action = modeRun
	if d.onStop != nil {
		d.onStop(d, reason)
	}
	d.mode = d.action
	if d.mode == modeStepOver || d.mode == modeStepOut {
		d.targetDepth = L.StackDepth()
	}
}

// shouldStop decides whether the current line is a stop point and why. A pending
// Pause and breakpoints take priority over an in-progress step.
func (d *Debugger) shouldStop(L *LState, line int) (StopReason, bool) {
	if atomic.SwapInt32(&d.pauseReq, 0) == 1 {
		return StopPause, true
	}
	if f, ok := L.Frame(0); ok && d.hasBreakpoint(f, line) {
		return StopBreakpoint, true
	}
	switch d.mode {
	case modeStepInto:
		return StopStep, true
	case modeStepOver:
		if L.StackDepth() <= d.targetDepth {
			return StopStep, true
		}
	case modeStepOut:
		if L.StackDepth() < d.targetDepth {
			return StopStep, true
		}
	}
	return StopStep, false
}

func (d *Debugger) hasBreakpoint(f Frame, line int) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.bps) == 0 {
		return false
	}
	if set := d.bps[f.Source()]; set != nil && set[line] {
		return true
	}
	if set := d.bps[f.ShortSource()]; set != nil && set[line] {
		return true
	}
	return false
}
