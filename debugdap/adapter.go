package debugdap

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"sync"

	luapure "github.com/htcom-code/lua-pure"
)

// Config configures a DAP session.
type Config struct {
	// NewState returns a fresh, library-loaded LState for each launched program.
	NewState func() *luapure.LState
	// Source resolves a program id to its Lua text — used to load a launched
	// program and to answer DAP 'source' requests. May be nil if launches always
	// carry their source inline (the 'program' launch arg as text via 'source').
	Source luapure.SourceResolver
}

// Serve runs one DAP session over conn until the client disconnects or the
// stream closes. It is the per-connection entry point (one Config-built session
// per connection).
func Serve(conn io.ReadWriter, cfg Config) error {
	a := &adapter{
		c:        newCodec(conn, conn),
		cfg:      cfg,
		bps:      map[string][]int{},
		inline:   map[string]string{},
		srcRefBy: map[string]int{},
	}
	return a.serve()
}

type adapter struct {
	c   *codec
	cfg Config

	// launch configuration, gathered before the program starts.
	bps        map[string][]int
	inline     map[string]string
	program    string
	programSrc string
	launchReq  bool // a launch request has arrived
	configured bool // configurationDone has arrived
	started    bool

	sess *luapure.Session
	done <-chan luapure.RunResult

	mu     sync.Mutex
	paused bool
	closed bool

	// variablesReference handles (reset each stop): ref = index+1.
	refMu   sync.Mutex
	handles []handle

	// sourceReference numbers for chunk ids (request goroutine only): ref = idx+1.
	srcRefBy map[string]int
	srcByRef []string
}

const (
	hLocals = iota // a frame's variable scope
	hValue         // an expandable table value
)

type handle struct {
	kind  int
	frame int
	val   luapure.Value
}

func (a *adapter) serve() error {
	for {
		req, err := a.c.read()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		a.handle(req)
		a.mu.Lock()
		done := a.closed
		a.mu.Unlock()
		if done {
			return nil
		}
	}
}

func (a *adapter) handle(req *request) {
	switch req.Command {
	case "initialize":
		a.c.send(respond(req, map[string]any{
			"supportsConfigurationDoneRequest": true,
			"supportsEvaluateForHovers":        true,
			"supportsTerminateRequest":         true,
		}))
		a.c.send(event("initialized", nil))
	case "launch", "attach":
		a.onLaunch(req)
	case "setBreakpoints":
		a.onSetBreakpoints(req)
	case "setExceptionBreakpoints":
		a.c.send(respond(req, map[string]any{"breakpoints": []any{}}))
	case "configurationDone":
		a.c.send(respond(req, nil))
		a.configured = true
		a.maybeStart()
	case "threads":
		a.c.send(respond(req, map[string]any{
			"threads": []any{map[string]any{"id": 1, "name": "main thread"}},
		}))
	case "stackTrace":
		a.onStackTrace(req)
	case "scopes":
		a.onScopes(req)
	case "variables":
		a.onVariables(req)
	case "evaluate":
		a.onEvaluate(req)
	case "source":
		a.onSource(req)
	case "continue":
		a.resume(req, (*luapure.Session).Continue, map[string]any{"allThreadsContinued": true})
	case "next":
		a.resume(req, (*luapure.Session).StepOver, nil)
	case "stepIn":
		a.resume(req, (*luapure.Session).StepInto, nil)
	case "stepOut":
		a.resume(req, (*luapure.Session).StepOut, nil)
	case "pause":
		if a.sess != nil {
			a.sess.Pause()
		}
		a.c.send(respond(req, nil))
	case "disconnect", "terminate":
		a.onDisconnect(req)
	default:
		a.c.send(respondErr(req, "unsupported request: "+req.Command))
	}
}

func (a *adapter) onLaunch(req *request) {
	var args struct {
		Program string `json:"program"`
		Source  string `json:"source"`
	}
	_ = json.Unmarshal(req.Arguments, &args)
	if args.Program == "" {
		a.c.send(respondErr(req, "launch: missing 'program'"))
		return
	}
	a.program = args.Program
	if args.Source != "" {
		a.inline[args.Program] = args.Source
	}
	a.launchReq = true
	a.c.send(respond(req, nil))
	a.maybeStart()
}

// maybeStart begins execution once both the launch request and
// configurationDone have arrived (the DAP launch handshake).
func (a *adapter) maybeStart() {
	if a.started || !a.launchReq || !a.configured {
		return
	}
	src, ok := a.resolveSource(a.program)
	if !ok {
		a.c.send(event("output", map[string]any{
			"category": "stderr",
			"output":   "no source for program: " + a.program + "\n",
		}))
		a.c.send(event("terminated", nil))
		return
	}
	a.programSrc = src
	a.started = true

	L := a.cfg.NewState()
	a.sess = luapure.NewSession(L, a.resolveSource)
	for s, lines := range a.bps {
		a.sess.SetBreakpoints(s, lines)
	}
	a.done = a.sess.Start(src, "="+a.program)
	go a.pump()
}

// pump translates Session stops/exit into DAP events on its own goroutine.
func (a *adapter) pump() {
	for {
		select {
		case st := <-a.sess.Stops():
			a.resetHandles()
			a.mu.Lock()
			a.paused = true
			a.mu.Unlock()
			a.c.send(event("stopped", map[string]any{
				"reason":            st.Reason.String(),
				"threadId":          1,
				"allThreadsStopped": true,
			}))
		case r := <-a.done:
			a.mu.Lock()
			a.paused = false
			a.mu.Unlock()
			if r.Err != nil {
				a.c.send(event("output", map[string]any{
					"category": "stderr", "output": r.Err.Error() + "\n",
				}))
			}
			a.c.send(event("terminated", nil))
			a.c.send(event("exited", map[string]any{"exitCode": exitCode(r)}))
			return
		}
	}
}

func exitCode(r luapure.RunResult) int {
	if r.Err != nil {
		return 1
	}
	return 0
}

func (a *adapter) onSetBreakpoints(req *request) {
	var args struct {
		Source struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"source"`
		Breakpoints []struct {
			Line int `json:"line"`
		} `json:"breakpoints"`
	}
	_ = json.Unmarshal(req.Arguments, &args)
	id := args.Source.Name
	if id == "" {
		id = args.Source.Path
	}
	lines := make([]int, 0, len(args.Breakpoints))
	verified := make([]any, 0, len(args.Breakpoints))
	for _, b := range args.Breakpoints {
		lines = append(lines, b.Line)
		verified = append(verified, map[string]any{"verified": true, "line": b.Line})
	}
	a.bps[id] = lines
	if a.sess != nil {
		a.sess.SetBreakpoints(id, lines)
	}
	a.c.send(respond(req, map[string]any{"breakpoints": verified}))
}

func (a *adapter) onStackTrace(req *request) {
	if !a.isPaused() {
		a.c.send(respondErr(req, "not stopped"))
		return
	}
	frames := a.sess.Stack()
	out := make([]any, 0, len(frames))
	for _, f := range frames {
		sf := map[string]any{
			"id":     f.Level,
			"name":   frameName(f),
			"line":   f.Line,
			"column": 1,
		}
		if src := a.sourceObject(f.Source); src != nil {
			sf["source"] = src
		}
		out = append(out, sf)
	}
	a.c.send(respond(req, map[string]any{"stackFrames": out, "totalFrames": len(out)}))
}

func frameName(f luapure.FrameInfo) string {
	if f.Func != "" {
		return f.Func
	}
	return "(" + f.Source + ")"
}

func (a *adapter) onScopes(req *request) {
	var args struct {
		FrameID int `json:"frameId"`
	}
	_ = json.Unmarshal(req.Arguments, &args)
	ref := a.addHandle(handle{kind: hLocals, frame: args.FrameID})
	a.c.send(respond(req, map[string]any{
		"scopes": []any{map[string]any{
			"name": "Locals", "variablesReference": ref, "expensive": false,
		}},
	}))
}

func (a *adapter) onVariables(req *request) {
	var args struct {
		VariablesReference int `json:"variablesReference"`
	}
	_ = json.Unmarshal(req.Arguments, &args)
	h, ok := a.getHandle(args.VariablesReference)
	if !ok {
		a.c.send(respond(req, map[string]any{"variables": []any{}}))
		return
	}
	var vars []any
	switch h.kind {
	case hLocals:
		for _, v := range a.sess.Variables(h.frame) {
			vars = append(vars, a.variable(v.Name, v.Value, v.Raw))
		}
	case hValue:
		vars = a.expand(h.val)
	}
	if vars == nil {
		vars = []any{}
	}
	a.c.send(respond(req, map[string]any{"variables": vars}))
}

// variable builds a DAP variable, assigning a child reference when the value is
// an expandable table.
func (a *adapter) variable(name, rendered string, raw luapure.Value) map[string]any {
	ref := 0
	if raw.IsTable() {
		ref = a.addHandle(handle{kind: hValue, val: raw})
	}
	return map[string]any{"name": name, "value": rendered, "variablesReference": ref}
}

// expand lists a table value's fields as DAP variables.
func (a *adapter) expand(v luapure.Value) []any {
	t := v.AsTable()
	if t == nil {
		return []any{}
	}
	var out []any
	key := luapure.Nil
	for {
		k, val, ok := t.Next(key)
		if !ok {
			break
		}
		key = k
		out = append(out, a.variable(renderKey(k), renderVal(val), val))
	}
	if out == nil {
		out = []any{}
	}
	return out
}

func (a *adapter) onEvaluate(req *request) {
	var args struct {
		Expression string `json:"expression"`
		FrameID    int    `json:"frameId"`
	}
	_ = json.Unmarshal(req.Arguments, &args)
	if !a.isPaused() {
		a.c.send(respondErr(req, "not stopped"))
		return
	}
	res, err := a.sess.Eval(args.FrameID, args.Expression)
	if err != nil {
		a.c.send(respondErr(req, err.Error()))
		return
	}
	a.c.send(respond(req, map[string]any{"result": res, "variablesReference": 0}))
}

func (a *adapter) onSource(req *request) {
	var args struct {
		SourceReference int `json:"sourceReference"`
		Source          struct {
			Name string `json:"name"`
		} `json:"source"`
	}
	_ = json.Unmarshal(req.Arguments, &args)
	id := ""
	if args.SourceReference >= 1 && args.SourceReference <= len(a.srcByRef) {
		id = a.srcByRef[args.SourceReference-1]
	} else {
		id = args.Source.Name
	}
	text, ok := a.resolveSource(id)
	if !ok {
		a.c.send(respondErr(req, "no source for: "+id))
		return
	}
	a.c.send(respond(req, map[string]any{"content": text}))
}

// resume issues a resume action and responds; the next stop/exit arrives via the
// pump as an event.
func (a *adapter) resume(req *request, action func(*luapure.Session), body any) {
	if !a.isPaused() {
		a.c.send(respondErr(req, "not stopped"))
		return
	}
	a.mu.Lock()
	a.paused = false
	a.mu.Unlock()
	action(a.sess)
	a.c.send(respond(req, body))
}

func (a *adapter) onDisconnect(req *request) {
	// Drain a paused program so its goroutine isn't left parked: drop hooks and
	// breakpoints, then let it run to completion.
	if a.sess != nil && a.isPaused() {
		a.sess.Debugger().ClearBreakpoints()
		a.sess.Debugger().Detach()
		a.mu.Lock()
		a.paused = false
		a.mu.Unlock()
		a.sess.Continue()
	}
	a.c.send(respond(req, nil))
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
}

// --- helpers ---

func (a *adapter) isPaused() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.paused
}

func (a *adapter) resolveSource(id string) (string, bool) {
	if t, ok := a.inline[id]; ok {
		return t, true
	}
	if a.cfg.Source != nil {
		return a.cfg.Source(id)
	}
	return "", false
}

// sourceObject builds a DAP Source for a chunk id, with a sourceReference the
// client uses to fetch text (so it needs no local copy). Returns nil when no
// source is available.
func (a *adapter) sourceObject(id string) map[string]any {
	if id == "" {
		return nil
	}
	if _, ok := a.resolveSource(id); !ok {
		return map[string]any{"name": id}
	}
	ref, ok := a.srcRefBy[id]
	if !ok {
		a.srcByRef = append(a.srcByRef, id)
		ref = len(a.srcByRef)
		a.srcRefBy[id] = ref
	}
	return map[string]any{"name": id, "sourceReference": ref}
}

func (a *adapter) resetHandles() {
	a.refMu.Lock()
	a.handles = a.handles[:0]
	a.refMu.Unlock()
}

func (a *adapter) addHandle(h handle) int {
	a.refMu.Lock()
	defer a.refMu.Unlock()
	a.handles = append(a.handles, h)
	return len(a.handles)
}

func (a *adapter) getHandle(ref int) (handle, bool) {
	a.refMu.Lock()
	defer a.refMu.Unlock()
	if ref < 1 || ref > len(a.handles) {
		return handle{}, false
	}
	return a.handles[ref-1], true
}

func renderKey(k luapure.Value) string {
	if k.IsString() {
		return k.Str()
	}
	return renderVal(k)
}

func renderVal(v luapure.Value) string {
	switch {
	case v.IsNil():
		return "nil"
	case v.IsBool():
		if v.AsBool() {
			return "true"
		}
		return "false"
	case v.IsInt():
		return strconv.FormatInt(v.AsInt(), 10)
	case v.IsFloat():
		return strconv.FormatFloat(v.AsFloat(), 'g', -1, 64)
	case v.IsString():
		return strconv.Quote(v.Str())
	case v.IsTable():
		return "table"
	case v.IsFunction():
		return "function"
	case v.IsThread():
		return "thread"
	case v.IsUserData():
		return "userdata"
	}
	return fmt.Sprintf("%v", v)
}
