package luapure

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Session is a synchronous façade over a Debugger for building a debug front
// end — an MCP server, a DAP adapter, a CLI console. The Debugger's stop
// handler runs on the VM goroutine and blocks it; Session lets a controller on
// another goroutine drive that paused VM with ordinary request/response calls,
// marshalling each inspection onto the VM goroutine so frame access stays safe.
//
// Source on the server only. A Session never needs the script text to debug:
// breakpoints and stop locations are keyed by a chunk's *name* (an opaque id
// such as "script/42") and line, and line numbers come from the compiled
// prototype. The source text is needed only to *display* where execution
// stopped, and that is served on demand from wherever the host keeps it (a
// database, an object store) through the SourceResolver — so a remote client
// that holds no source can still show context by asking the Session. This
// mirrors DAP's sourceReference mechanism.
//
// Protocol. Launch with Start and select over Stops() and the returned result
// channel. On each StopState, inspect with Stack / Variables / Eval, then issue
// exactly one resume (Continue / StepInto / StepOver / StepOut). Inspection and
// resume calls are valid only while paused at a stop — between receiving a
// StopState and resuming. SetBreakpoints and Pause are safe to call anytime.
type Session struct {
	L       *LState
	d       *Debugger
	resolve SourceResolver

	stops chan StopState
	reqs  chan sessReq
}

// SourceResolver returns the text of the chunk named id (its short name, e.g.
// "script/42"), or ok=false when unknown. A host backs it with a DB lookup.
type SourceResolver func(id string) (text string, ok bool)

// StopReason values are reused from the Debugger. StopState describes one stop.
type StopState struct {
	Reason StopReason
	Source string // innermost frame's chunk id (short name); "" for a native frame
	Line   int    // innermost frame's current line (-1 if unavailable)
	Func   string // innermost frame's function name, if known
	Depth  int    // number of active frames
}

// FrameInfo is a snapshot of one call frame (Session.Stack).
type FrameInfo struct {
	Level  int
	Source string // chunk id (short name)
	Line   int
	Func   string
	What   string // namewhat: "local", "global", "method", "" ...
}

// Var is a rendered variable binding (Session.Variables). Raw carries the
// underlying value so a front end can render it differently or expand a table
// into its fields (e.g. a DAP variablesReference tree); it is valid only while
// the program is paused at the stop the Var was read from.
type Var struct {
	Name  string
	Value string // display rendering of the value
	Kind  string // "local" | "upvalue" | "vararg"
	Raw   Value
}

type sessReq struct {
	fn     func(*Debugger)
	resume bool
	done   chan struct{}
}

// NewSession attaches a debug session to L. resolve may be nil (no source
// serving). It installs the debugger's hook; the program runs only once Start
// (or the host's own call into the VM) executes a chunk.
func NewSession(L *LState, resolve SourceResolver) *Session {
	s := &Session{
		L:       L,
		resolve: resolve,
		stops:   make(chan StopState),
		reqs:    make(chan sessReq),
	}
	s.d = NewDebugger(L)
	s.d.OnStop(s.onStop)
	return s
}

// Debugger exposes the underlying controller (e.g. for ClearBreakpoints/Detach).
func (s *Session) Debugger() *Debugger { return s.d }

// Stops delivers a StopState each time the program pauses. Receive from it in
// the controller loop.
func (s *Session) Stops() <-chan StopState { return s.stops }

// RunResult carries a finished run's results or error (Session.Start).
type RunResult struct {
	Values []Value
	Err    error
}

// Start runs src as a chunk named chunkName on a new goroutine and returns a
// channel that receives the single result when it finishes. chunkName is the
// debug identity of the script: prefix it with "=" so its short name (which
// breakpoints and StopState use) is the bare id, e.g. "=script/42".
func (s *Session) Start(src, chunkName string) <-chan RunResult {
	out := make(chan RunResult, 1)
	go func() {
		res, err := s.L.DoString(src, chunkName)
		out <- RunResult{Values: res, Err: err}
	}()
	return out
}

// --- breakpoints / pause (safe anytime) ---

// SetBreakpoints replaces the breakpoints for source (a chunk's short id).
func (s *Session) SetBreakpoints(source string, lines []int) { s.d.SetBreakpoints(source, lines) }

// Pause asks the program to stop at the next line.
func (s *Session) Pause() { s.d.Pause() }

// --- inspection (call only while paused) ---

// Stack returns the call stack at the current stop, innermost first.
func (s *Session) Stack() []FrameInfo {
	var out []FrameInfo
	s.onVM(func(d *Debugger) {
		for i, f := range d.Frames() {
			name, what := f.FuncName()
			out = append(out, FrameInfo{
				Level: i, Source: f.ShortSource(), Line: f.CurrentLine(),
				Func: name, What: what,
			})
		}
	})
	return out
}

// Variables returns the rendered locals, upvalues and varargs of the frame at
// level (0 = innermost).
func (s *Session) Variables(level int) []Var {
	var out []Var
	s.onVM(func(d *Debugger) {
		f, ok := d.L.Frame(level)
		if !ok {
			return
		}
		for _, lv := range f.Locals() {
			out = append(out, Var{Name: lv.Name, Value: renderValue(lv.Value), Kind: "local", Raw: lv.Value})
		}
		for n := 1; ; n++ {
			nm, v, ok := f.Upvalue(n)
			if !ok {
				break
			}
			out = append(out, Var{Name: nm, Value: renderValue(v), Kind: "upvalue", Raw: v})
		}
		for n := 1; ; n++ {
			v, ok := f.Vararg(n)
			if !ok {
				break
			}
			out = append(out, Var{Name: fmt.Sprintf("(vararg %d)", n), Value: renderValue(v), Kind: "vararg", Raw: v})
		}
	})
	return out
}

// Eval evaluates a Lua expression (or statement) in the scope of the frame at
// level and returns its results rendered for display (tab-separated).
func (s *Session) Eval(level int, expr string) (string, error) {
	var rendered string
	var evalErr error
	s.onVM(func(d *Debugger) {
		f, ok := d.L.Frame(level)
		if !ok {
			evalErr = errors.New("no such frame")
			return
		}
		res, err := f.Eval(expr)
		if err != nil {
			evalErr = err
			return
		}
		parts := make([]string, len(res))
		for i, v := range res {
			parts[i] = renderValue(v)
		}
		rendered = strings.Join(parts, "\t")
	})
	return rendered, evalErr
}

// --- resume (call once per stop) ---

// Continue resumes until the next breakpoint or Pause.
func (s *Session) Continue() { s.resume((*Debugger).Continue) }

// StepInto resumes to the next line, descending into calls.
func (s *Session) StepInto() { s.resume((*Debugger).StepInto) }

// StepOver resumes to the next line at this depth or shallower.
func (s *Session) StepOver() { s.resume((*Debugger).StepOver) }

// StepOut resumes until execution returns to a shallower frame.
func (s *Session) StepOut() { s.resume((*Debugger).StepOut) }

// --- source serving (no VM goroutine needed; the host's DB does the work) ---

// Source returns the text of chunk id via the SourceResolver, or ok=false when
// there is no resolver or the id is unknown. A remote client that holds no
// source calls this to display where it stopped.
func (s *Session) Source(id string) (text string, ok bool) {
	if s.resolve == nil {
		return "", false
	}
	return s.resolve(id)
}

// Snippet renders the lines of chunk id around line (± ctx lines), each prefixed
// with its number and a marker ("->") on the current line — enough for a front
// end to show the stop location without ever holding the source itself.
func (s *Session) Snippet(id string, line, ctx int) (string, bool) {
	text, ok := s.Source(id)
	if !ok {
		return "", false
	}
	lines := strings.Split(text, "\n")
	lo, hi := line-ctx, line+ctx
	if lo < 1 {
		lo = 1
	}
	if hi > len(lines) {
		hi = len(lines)
	}
	var b strings.Builder
	for n := lo; n <= hi; n++ {
		marker := "  "
		if n == line {
			marker = "->"
		}
		fmt.Fprintf(&b, "%s %d\t%s\n", marker, n, lines[n-1])
	}
	return b.String(), true
}

// --- internals: marshal work onto the VM goroutine while it is parked ---

func (s *Session) onStop(d *Debugger, reason StopReason) {
	st := StopState{Reason: reason, Depth: d.L.StackDepth()}
	if f, ok := d.L.Frame(0); ok {
		st.Source = f.ShortSource()
		st.Line = f.CurrentLine()
		st.Func, _ = f.FuncName()
	}
	s.stops <- st
	for {
		r := <-s.reqs
		if r.fn != nil {
			r.fn(d)
		}
		if r.done != nil {
			close(r.done)
		}
		if r.resume {
			return
		}
	}
}

// onVM runs fn on the VM goroutine (which is parked in onStop) and waits for it.
func (s *Session) onVM(fn func(*Debugger)) {
	done := make(chan struct{})
	s.reqs <- sessReq{fn: fn, done: done}
	<-done
}

// resume applies a resume action on the VM goroutine and lets it continue.
func (s *Session) resume(action func(*Debugger)) {
	s.reqs <- sessReq{fn: action, resume: true}
}

// renderValue formats a value for a debugger display: scalars literally,
// everything else as its type name (no __tostring, so it has no side effects).
func renderValue(v Value) string {
	switch v.tag {
	case tagNil:
		return "nil"
	case tagTrue:
		return "true"
	case tagFalse:
		return "false"
	case tagInt, tagFloat:
		return numToString(v)
	case tagString:
		return strconv.Quote(v.Str())
	default:
		return typeName(v)
	}
}
