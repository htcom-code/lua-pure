package debugmcp

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"sync"

	luapure "github.com/htcom-code/lua-pure"
)

// Server exposes a luapure debug session over MCP. Construct it with a state
// factory and a source resolver, then run Serve over a Transport.
//
// Lifecycle. The debuggee runs on its own goroutine; the control tools (launch,
// continue, step_*) block until the program next stops (a breakpoint) or
// finishes, returning that outcome, while the inspection tools (stack,
// variables, evaluate, get_source) read the paused state. This maps the
// inherently stateful debug loop onto MCP's request/response model: one tool
// call advances or queries, exactly once.
type Server struct {
	// NewState returns a fresh, library-loaded LState for each launch. Required.
	NewState func() *luapure.LState
	// Source resolves a program id to its Lua source text — used to load a
	// program launched by id, and to serve display context for get_source. May
	// be nil if every launch passes its source inline.
	Source luapure.SourceResolver
	// Name and Version are reported to the client at initialize.
	Name    string
	Version string

	mu       sync.Mutex
	sess     *luapure.Session
	done     <-chan luapure.RunResult
	state    runState
	lastStop luapure.StopState
	bps      map[string][]int // breakpoints, applied to each launch
	inline   map[string]string
}

// MCP protocol versions this server speaks. Our surface (initialize, tools and
// structuredContent) is unchanged across these revisions, so we negotiate the
// version string only: echo the client's when supported, else answer with our
// latest and let the client decide. latestProtocol is also the default when the
// client sends none.
const latestProtocol = "2025-11-25"

var supportedProtocols = map[string]bool{
	"2025-11-25": true,
	"2025-06-18": true,
}

type runState uint8

const (
	stateIdle     runState = iota // no program launched
	stateRunning                  // a control op is resuming/awaiting the next stop
	statePaused                   // stopped at a breakpoint/step
	stateFinished                 // program ended
)

// Serve runs the MCP message loop over t until the peer closes (io.EOF) or a
// transport error occurs. It is the blocking entry point.
func (s *Server) Serve(t Transport) error {
	s.once()
	for {
		msg, err := t.Read()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if len(bytes.TrimSpace(msg)) == 0 {
			continue
		}
		if reply := s.HandleMessage(msg); reply != nil {
			_ = t.Write(reply)
		}
	}
}

// HandleMessage processes one JSON-RPC message and returns the reply frame, or
// nil for a notification (no reply). It is the shared core behind the stdio/TCP
// Serve loop and the HTTP Handler. Safe for concurrent calls (the HTTP server
// invokes it per request); blocking control tools do not hold the server lock,
// so a pause can land while a continue is in flight.
func (s *Server) HandleMessage(msg []byte) []byte {
	s.once()
	var req rpcMessage
	if err := json.Unmarshal(msg, &req); err != nil {
		return encodeError(nil, errf(codeParse, "parse error"))
	}
	result, rerr := s.dispatch(req.Method, req.Params)
	if req.ID == nil {
		return nil // notification
	}
	if rerr != nil {
		return encodeError(req.ID, rerr)
	}
	return encodeResult(req.ID, result)
}

// once lazily initialises the maps (Serve and HandleMessage are both entry
// points).
func (s *Server) once() {
	s.mu.Lock()
	if s.bps == nil {
		s.bps = map[string][]int{}
	}
	if s.inline == nil {
		s.inline = map[string]string{}
	}
	s.mu.Unlock()
}

func (s *Server) dispatch(method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return s.initialize(params), nil
	case "notifications/initialized", "initialized":
		return nil, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": toolDefs()}, nil
	case "tools/call":
		return s.toolsCall(params)
	default:
		return nil, errf(codeMethodNotFound, "unknown method: "+method)
	}
}

func (s *Server) initialize(params json.RawMessage) any {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)
	// Echo the client's version when we support it; otherwise downgrade to our
	// latest rather than claim support for an unknown revision.
	ver := p.ProtocolVersion
	if !supportedProtocols[ver] {
		ver = latestProtocol
	}
	name, version := s.Name, s.Version
	if name == "" {
		name = "luapure-debug"
	}
	if version == "" {
		version = "0.1.0"
	}
	return map[string]any{
		"protocolVersion": ver,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": name, "version": version},
	}
}

func (s *Server) toolsCall(params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, errf(codeInvalidParams, err.Error())
	}
	run := toolHandlers[p.Name]
	if run == nil {
		return nil, errf(codeInvalidParams, "unknown tool: "+p.Name)
	}
	// No blanket lock: control tools block until the next stop, and must not
	// hold the server lock while doing so (so pause/set_breakpoints stay
	// responsive). Each handler does its own short-lived locking.
	payload, toolErr := run(s, p.Arguments)
	if toolErr != "" {
		// MCP convention: a tool's own failure is a normal result with isError,
		// not a protocol-level error.
		return toolResult(map[string]any{"error": toolErr}, true), nil
	}
	return toolResult(payload, false), nil
}

// toolResult wraps a payload as an MCP tools/call result: a JSON text block plus
// the structured object (for clients that read structuredContent).
func toolResult(payload any, isError bool) any {
	text, _ := json.MarshalIndent(payload, "", "  ")
	return map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": string(text)}},
		"isError":           isError,
		"structuredContent": payload,
	}
}

// --- value rendering for results ---

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
	return "?"
}

// argInt reads an integer argument (JSON numbers arrive as float64), with a
// default when missing.
func argInt(args map[string]any, key string, def int) int {
	if v, ok := args[key]; ok {
		if f, ok := v.(float64); ok {
			return int(f)
		}
	}
	return def
}

func argStr(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func argIntSlice(args map[string]any, key string) []int {
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	out := make([]int, 0, len(raw))
	for _, e := range raw {
		if f, ok := e.(float64); ok {
			out = append(out, int(f))
		}
	}
	return out
}
