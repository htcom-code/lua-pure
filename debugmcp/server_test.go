package debugmcp

import (
	"encoding/json"
	"io"
	"testing"

	luapure "github.com/htcom-code/lua-pure/lua"
)

// chanTransport is an in-memory Transport for tests: requests go in, responses
// come out.
type chanTransport struct {
	in     chan []byte
	out    chan []byte
	closed chan struct{}
}

func newChanTransport() *chanTransport {
	return &chanTransport{in: make(chan []byte, 8), out: make(chan []byte, 8), closed: make(chan struct{})}
}

func (c *chanTransport) Read() ([]byte, error) {
	select {
	case m, ok := <-c.in:
		if !ok {
			return nil, io.EOF
		}
		return m, nil
	case <-c.closed:
		return nil, io.EOF
	}
}

func (c *chanTransport) Write(m []byte) error {
	b := make([]byte, len(m))
	copy(b, m)
	c.out <- b
	return nil
}

func (c *chanTransport) Close() error { return nil }

// testClient drives a server over a chanTransport.
type testClient struct {
	t  *testing.T
	tr *chanTransport
	id int
}

// call sends a request and returns its "result" object.
func (c *testClient) call(method string, params map[string]any) map[string]any {
	c.t.Helper()
	c.id++
	req := map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method}
	if params != nil {
		req["params"] = params
	}
	b, _ := json.Marshal(req)
	c.tr.in <- b
	resp := <-c.tr.out
	var r struct {
		Result map[string]any `json:"result"`
		Error  *rpcError      `json:"error"`
	}
	if err := json.Unmarshal(resp, &r); err != nil {
		c.t.Fatalf("bad response: %v (%s)", err, resp)
	}
	if r.Error != nil {
		c.t.Fatalf("%s returned error: %s", method, r.Error.Message)
	}
	return r.Result
}

// callTool calls a tool and returns its structuredContent payload.
func (c *testClient) callTool(name string, args map[string]any) map[string]any {
	c.t.Helper()
	res := c.call("tools/call", map[string]any{"name": name, "arguments": args})
	sc, _ := res["structuredContent"].(map[string]any)
	if sc == nil {
		c.t.Fatalf("tool %s: no structuredContent in %v", name, res)
	}
	return sc
}

const prog = "local function add(a, b)\n" + // 1
	"  return a + b\n" + // 2 (breakpoint: inside add)
	"end\n" + // 3
	"local t = 0\n" + // 4
	"for i = 1, 3 do\n" + // 5
	"  t = add(t, i)\n" + // 6 (breakpoint: call site)
	"end\n" + // 7
	"return t\n" // 8

func newTestServer() *Server {
	db := map[string]string{"loop": prog}
	return &Server{
		NewState: func() *luapure.LState {
			L := luapure.NewState()
			L.OpenLibs()
			return L
		},
		Source: func(id string) (string, bool) { s, ok := db[id]; return s, ok },
		Name:   "test", Version: "0",
	}
}

func TestMCPHandshakeAndTools(t *testing.T) {
	srv := newTestServer()
	tr := newChanTransport()
	go srv.Serve(tr)
	c := &testClient{t: t, tr: tr}

	init := c.call("initialize", map[string]any{"protocolVersion": "2025-06-18"})
	if init["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v", init["protocolVersion"])
	}
	si, _ := init["serverInfo"].(map[string]any)
	if si["name"] != "test" {
		t.Errorf("serverInfo.name = %v", si["name"])
	}

	tools := c.call("tools/list", nil)
	list, _ := tools["tools"].([]any)
	want := map[string]bool{"launch": true, "continue": true, "stack": true, "evaluate": true, "get_source": true}
	for _, e := range list {
		m, _ := e.(map[string]any)
		delete(want, m["name"].(string))
	}
	if len(want) != 0 {
		t.Errorf("tools/list missing: %v", want)
	}
}

// Version negotiation: a supported version is echoed; an unknown or missing one
// is answered with our latest rather than claimed as supported.
func TestMCPProtocolNegotiation(t *testing.T) {
	cases := []struct{ ask, want string }{
		{"2025-11-25", "2025-11-25"},   // latest, supported -> echo
		{"2025-06-18", "2025-06-18"},   // older, supported -> echo
		{"2099-01-01", latestProtocol}, // unknown future -> downgrade
		{"", latestProtocol},           // missing -> latest
	}
	for _, c := range cases {
		srv := newTestServer()
		tr := newChanTransport()
		go srv.Serve(tr)
		cl := &testClient{t: t, tr: tr}
		params := map[string]any{}
		if c.ask != "" {
			params["protocolVersion"] = c.ask
		}
		init := cl.call("initialize", params)
		if init["protocolVersion"] != c.want {
			t.Errorf("ask %q: protocolVersion = %v, want %v", c.ask, init["protocolVersion"], c.want)
		}
	}
}

// Full debug session over MCP: breakpoint, inspect, evaluate, step the loop to
// completion. The client never holds the source — it launches by id and fetches
// a snippet from the server.
func TestMCPDebugSession(t *testing.T) {
	srv := newTestServer()
	tr := newChanTransport()
	go srv.Serve(tr)
	c := &testClient{t: t, tr: tr}

	c.call("initialize", map[string]any{})
	c.callTool("set_breakpoints", map[string]any{"source": "loop", "lines": []any{6.0}})

	// Launch by id (source pulled from the server's DB).
	ev := c.callTool("launch", map[string]any{"program": "loop"})
	var iters []string
	for {
		switch ev["event"] {
		case "stopped":
			if ev["line"].(float64) != 6 {
				t.Fatalf("stopped at line %v, want 6", ev["line"])
			}
			if ev["source"] != "loop" {
				t.Fatalf("stopped source %v, want loop", ev["source"])
			}
			// Snippet fetched from the server (client has no source).
			snip := c.callTool("get_source", map[string]any{"id": "loop", "line": 6.0, "context": 1.0})
			if s, _ := snip["snippet"].(string); s == "" {
				t.Error("empty snippet")
			}
			// Evaluate the loop counter in scope.
			r := c.callTool("evaluate", map[string]any{"expr": "i"})
			iters = append(iters, r["result"].(string))
			ev = c.callTool("continue", nil)
		case "finished":
			if e, ok := ev["error"]; ok {
				t.Fatalf("program errored: %v", e)
			}
			results, _ := ev["results"].([]any)
			if len(results) != 1 || results[0].(string) != "6" {
				t.Fatalf("results = %v, want [6]", results)
			}
			if len(iters) != 3 || iters[0] != "1" || iters[2] != "3" {
				t.Fatalf("loop counters via evaluate = %v, want [1 2 3]", iters)
			}
			return
		default:
			t.Fatalf("unexpected event: %v", ev)
		}
	}
}

// stack and variables report the paused frame for a deeper stop.
func TestMCPStackVariables(t *testing.T) {
	srv := newTestServer()
	tr := newChanTransport()
	go srv.Serve(tr)
	c := &testClient{t: t, tr: tr}
	c.call("initialize", map[string]any{})

	// Break inside add() (line 2).
	c.callTool("set_breakpoints", map[string]any{"source": "loop", "lines": []any{2.0}})
	ev := c.callTool("launch", map[string]any{"program": "loop"})
	if ev["event"] != "stopped" {
		t.Fatalf("expected stop inside add(), got %v", ev)
	}
	st := c.callTool("stack", nil)
	frames, _ := st["frames"].([]any)
	if len(frames) < 2 {
		t.Fatalf("stack depth %d, want >= 2", len(frames))
	}
	f0, _ := frames[0].(map[string]any)
	if f0["function"] != "add" {
		t.Errorf("innermost function = %v, want add", f0["function"])
	}
	vars := c.callTool("variables", map[string]any{"frame": 0.0})
	got := map[string]string{}
	for _, v := range vars["variables"].([]any) {
		m := v.(map[string]any)
		got[m["name"].(string)] = m["value"].(string)
	}
	if got["a"] == "" || got["b"] == "" {
		t.Errorf("expected locals a,b in add(); got %v", got)
	}
}
