package debugmcp

import (
	"fmt"
	"strings"

	luapure "github.com/htcom-code/lua-pure"
)

// toolHandler runs a tool. A non-empty second return is a tool-level error
// message (surfaced to the client as an isError result, not a protocol error).
type toolHandler func(s *Server, args map[string]any) (payload any, toolErr string)

var toolHandlers = map[string]toolHandler{
	"set_breakpoints": toolSetBreakpoints,
	"launch":          toolLaunch,
	"continue":        toolContinue,
	"step_over":       toolStep((*luapure.Session).StepOver),
	"step_into":       toolStep((*luapure.Session).StepInto),
	"step_out":        toolStep((*luapure.Session).StepOut),
	"pause":           toolPause,
	"stack":           toolStack,
	"variables":       toolVariables,
	"evaluate":        toolEvaluate,
	"get_source":      toolGetSource,
}

// toolDefs is the tools/list payload: each tool's name, description and JSON
// Schema for its arguments.
func toolDefs() []map[string]any {
	obj := func(props map[string]any, required ...string) map[string]any {
		schema := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	str := map[string]any{"type": "string"}
	intt := map[string]any{"type": "integer"}
	return []map[string]any{
		{
			"name":        "set_breakpoints",
			"description": "Set the breakpoints for a source (a program id). Replaces any previous set for that source. Safe before or during a run.",
			"inputSchema": obj(map[string]any{
				"source": str,
				"lines":  map[string]any{"type": "array", "items": intt},
			}, "source", "lines"),
		},
		{
			"name":        "launch",
			"description": "Compile and run a program, stopping at the first breakpoint. Returns either a 'stopped' event (with source/line) or a 'finished' event. Pass 'source' inline, or omit it to load by 'program' id from the server.",
			"inputSchema": obj(map[string]any{
				"program": str,
				"source":  str,
			}, "program"),
		},
		{"name": "continue", "description": "Resume until the next breakpoint or program end. Returns a 'stopped' or 'finished' event.", "inputSchema": obj(nil)},
		{"name": "step_over", "description": "Step to the next line in the current frame (calls run without stopping). Returns a 'stopped' or 'finished' event.", "inputSchema": obj(nil)},
		{"name": "step_into", "description": "Step to the next line, descending into calls. Returns a 'stopped' or 'finished' event.", "inputSchema": obj(nil)},
		{"name": "step_out", "description": "Run until the current function returns. Returns a 'stopped' or 'finished' event.", "inputSchema": obj(nil)},
		{"name": "pause", "description": "Request a stop at the next line (asynchronous).", "inputSchema": obj(nil)},
		{"name": "stack", "description": "List the call stack at the current stop (innermost first).", "inputSchema": obj(nil)},
		{
			"name":        "variables",
			"description": "List the locals, upvalues and varargs of a frame at the current stop.",
			"inputSchema": obj(map[string]any{"frame": intt}),
		},
		{
			"name":        "evaluate",
			"description": "Evaluate a Lua expression (or statement) in the scope of a frame at the current stop.",
			"inputSchema": obj(map[string]any{"expr": str, "frame": intt}, "expr"),
		},
		{
			"name":        "get_source",
			"description": "Fetch source text by program id — the whole text, or a snippet around 'line' when given. Defaults to the current stop's source. Lets a client with no local source show where it is.",
			"inputSchema": obj(map[string]any{"id": str, "line": intt, "context": intt}),
		},
	}
}

// --- handlers ---

func toolSetBreakpoints(s *Server, args map[string]any) (any, string) {
	source := argStr(args, "source")
	if source == "" {
		return nil, "missing 'source'"
	}
	lines := argIntSlice(args, "lines")
	// Concurrent-safe: store for the next launch, and apply live if running.
	s.mu.Lock()
	s.bps[source] = lines
	sess := s.sess
	s.mu.Unlock()
	if sess != nil {
		sess.SetBreakpoints(source, lines)
	}
	return map[string]any{"source": source, "lines": lines}, ""
}

func toolLaunch(s *Server, args map[string]any) (any, string) {
	if s.NewState == nil {
		return nil, "server misconfigured: NewState is nil"
	}
	program := argStr(args, "program")
	if program == "" {
		return nil, "missing 'program'"
	}
	// Resolve the source outside the lock (it may hit a slow store).
	src := argStr(args, "source")
	if src != "" {
		s.mu.Lock()
		s.inline[program] = src
		s.mu.Unlock()
	} else {
		t, ok := s.resolveSource(program)
		if !ok {
			return nil, "no source for program: " + program
		}
		src = t
	}

	s.mu.Lock()
	if s.state == stateRunning {
		s.mu.Unlock()
		return nil, "a debug operation is already in progress"
	}
	if s.state == statePaused {
		s.mu.Unlock()
		return nil, "a program is already paused; continue it before launching again"
	}
	L := s.NewState()
	sess := luapure.NewSession(L, func(id string) (string, bool) { return s.resolveSource(id) })
	bps := make(map[string][]int, len(s.bps))
	for k, v := range s.bps {
		bps[k] = v
	}
	s.sess = sess
	s.state = stateRunning
	s.mu.Unlock()

	for k, lines := range bps {
		sess.SetBreakpoints(k, lines)
	}
	done := sess.Start(src, "="+program)
	s.mu.Lock()
	s.done = done
	s.mu.Unlock()
	return s.waitFor(sess, done), ""
}

func toolContinue(s *Server, args map[string]any) (any, string) { return s.resumeAndWait((*luapure.Session).Continue) }

func toolStep(step func(*luapure.Session)) toolHandler {
	return func(s *Server, args map[string]any) (any, string) { return s.resumeAndWait(step) }
}

// resumeAndWait issues a resume action while paused, then waits (without holding
// the lock) for the next stop or program end.
func (s *Server) resumeAndWait(action func(*luapure.Session)) (any, string) {
	s.mu.Lock()
	if s.state != statePaused {
		st := s.state
		s.mu.Unlock()
		return nil, notPausedMsg(st)
	}
	sess, done := s.sess, s.done
	s.state = stateRunning
	s.mu.Unlock()

	action(sess)
	return s.waitFor(sess, done), ""
}

func toolPause(s *Server, args map[string]any) (any, string) {
	s.mu.Lock()
	sess := s.sess
	s.mu.Unlock()
	if sess == nil {
		return nil, "no active session"
	}
	sess.Pause()
	return map[string]any{"ok": true}, ""
}

func toolStack(s *Server, args map[string]any) (any, string) {
	sess, msg := s.pausedSession()
	if msg != "" {
		return nil, msg
	}
	frames := sess.Stack()
	out := make([]map[string]any, len(frames))
	for i, f := range frames {
		out[i] = map[string]any{
			"level": f.Level, "source": f.Source, "line": f.Line,
			"function": f.Func, "what": f.What,
		}
	}
	return map[string]any{"frames": out}, ""
}

func toolVariables(s *Server, args map[string]any) (any, string) {
	sess, msg := s.pausedSession()
	if msg != "" {
		return nil, msg
	}
	vars := sess.Variables(argInt(args, "frame", 0))
	out := make([]map[string]any, len(vars))
	for i, v := range vars {
		out[i] = map[string]any{"name": v.Name, "value": v.Value, "kind": v.Kind}
	}
	return map[string]any{"variables": out}, ""
}

func toolEvaluate(s *Server, args map[string]any) (any, string) {
	sess, msg := s.pausedSession()
	if msg != "" {
		return nil, msg
	}
	expr := argStr(args, "expr")
	if expr == "" {
		return nil, "missing 'expr'"
	}
	res, err := sess.Eval(argInt(args, "frame", 0), expr)
	if err != nil {
		return nil, err.Error()
	}
	return map[string]any{"result": res}, ""
}

func toolGetSource(s *Server, args map[string]any) (any, string) {
	id := argStr(args, "id")
	if id == "" {
		s.mu.Lock()
		if s.state == statePaused {
			id = s.lastStop.Source
		}
		s.mu.Unlock()
	}
	if id == "" {
		return nil, "missing 'id'"
	}
	text, ok := s.resolveSource(id)
	if !ok {
		return nil, "no source for: " + id
	}
	if line := argInt(args, "line", 0); line > 0 {
		return map[string]any{
			"id": id, "line": line,
			"snippet": makeSnippet(text, line, argInt(args, "context", 3)),
		}, ""
	}
	return map[string]any{"id": id, "source": text}, ""
}

// --- helpers ---

// pausedSession returns the session iff the program is paused, else an error
// message. Inspection tools must run only while paused (the VM is parked then).
func (s *Server) pausedSession() (*luapure.Session, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != statePaused {
		return nil, notPausedMsg(s.state)
	}
	return s.sess, ""
}

func notPausedMsg(st runState) string {
	switch st {
	case stateRunning:
		return "a debug operation is already in progress"
	case stateFinished:
		return "the program has finished; launch again to debug"
	default:
		return "no paused program; launch one first"
	}
}

// resolveSource reads the inline override (briefly locked) then falls back to
// the configured resolver (unlocked, as it may be slow).
func (s *Server) resolveSource(id string) (string, bool) {
	s.mu.Lock()
	t, ok := s.inline[id]
	s.mu.Unlock()
	if ok {
		return t, true
	}
	if s.Source != nil {
		return s.Source(id)
	}
	return "", false
}

// waitFor blocks (without the lock) until the program next stops or finishes,
// updating state under a brief lock and returning the matching event payload.
func (s *Server) waitFor(sess *luapure.Session, done <-chan luapure.RunResult) any {
	select {
	case st := <-sess.Stops():
		s.mu.Lock()
		s.lastStop = st
		s.state = statePaused
		s.mu.Unlock()
		return map[string]any{
			"event": "stopped", "reason": st.Reason.String(),
			"source": st.Source, "line": st.Line, "function": st.Func, "depth": st.Depth,
		}
	case r := <-done:
		s.mu.Lock()
		s.state = stateFinished
		s.mu.Unlock()
		m := map[string]any{"event": "finished"}
		if r.Err != nil {
			m["error"] = r.Err.Error()
		}
		vals := make([]string, len(r.Values))
		for i, v := range r.Values {
			vals[i] = renderVal(v)
		}
		m["results"] = vals
		return m
	}
}

func makeSnippet(text string, line, ctx int) string {
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
	return b.String()
}
