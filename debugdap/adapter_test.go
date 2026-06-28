package debugdap

import (
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	luapure "github.com/htcom-code/lua-pure/lua"
)

// dapClient drives an adapter over a pipe, demultiplexing async events from
// request responses (a continue's response and the next 'stopped' event can
// arrive in either order).
type dapClient struct {
	t    *testing.T
	c    *codec
	seq  int
	resp chan map[string]any
	evt  chan map[string]any
}

func newDAPClient(t *testing.T, conn net.Conn) *dapClient {
	cl := &dapClient{
		t:    t,
		c:    newCodec(conn, conn),
		seq:  1,
		resp: make(chan map[string]any, 16),
		evt:  make(chan map[string]any, 16),
	}
	go cl.readLoop()
	return cl
}

func (cl *dapClient) readLoop() {
	for {
		body, err := cl.c.readFrame()
		if err != nil {
			close(cl.resp)
			close(cl.evt)
			return
		}
		var m map[string]any
		if json.Unmarshal(body, &m) != nil {
			continue
		}
		switch m["type"] {
		case "response":
			cl.resp <- m
		case "event":
			cl.evt <- m
		}
	}
}

func (cl *dapClient) send(command string, args any) {
	cl.t.Helper()
	cl.seq++
	msg := map[string]any{"seq": cl.seq, "type": "request", "command": command}
	if args != nil {
		msg["arguments"] = args
	}
	b, _ := json.Marshal(msg)
	if _, err := fmt.Fprintf(cl.c.w, "Content-Length: %d\r\n\r\n", len(b)); err != nil {
		cl.t.Fatal(err)
	}
	cl.c.w.Write(b)
}

// req sends a command and returns the matching response (success asserted).
func (cl *dapClient) req(command string, args any) map[string]any {
	cl.t.Helper()
	cl.send(command, args)
	select {
	case r := <-cl.resp:
		if r["success"] != true {
			cl.t.Fatalf("%s failed: %v", command, r["message"])
		}
		body, _ := r["body"].(map[string]any)
		return body
	case <-time.After(5 * time.Second):
		cl.t.Fatalf("%s: no response", command)
		return nil
	}
}

// waitEvent returns the next event named name (skipping others).
func (cl *dapClient) waitEvent(name string) map[string]any {
	cl.t.Helper()
	for {
		select {
		case e, ok := <-cl.evt:
			if !ok {
				cl.t.Fatalf("connection closed waiting for %q event", name)
			}
			if e["event"] == name {
				body, _ := e["body"].(map[string]any)
				return body
			}
		case <-time.After(5 * time.Second):
			cl.t.Fatalf("timed out waiting for %q event", name)
			return nil
		}
	}
}

const dapProg = "local cfg = {x = 10, y = 20}\n" + // 1
	"local function add(a, b)\n" + // 2
	"  return a + b\n" + // 3
	"end\n" + // 4
	"local t = 0\n" + // 5
	"for i = 1, 3 do\n" + // 6
	"  t = add(t, i)\n" + // 7 (breakpoint)
	"end\n" + // 8
	"return t\n" // 9

func startAdapter(t *testing.T) *dapClient {
	t.Helper()
	server, client := net.Pipe()
	cfg := Config{
		NewState: func() *luapure.LState { L := luapure.NewState(); L.OpenLibs(); return L },
		Source:   func(id string) (string, bool) { return map[string]string{"loop": dapProg}[id], id == "loop" },
	}
	go Serve(server, cfg)
	t.Cleanup(func() { client.Close(); server.Close() })
	return newDAPClient(t, client)
}

func TestDAPFullSession(t *testing.T) {
	cl := startAdapter(t)

	cl.req("initialize", map[string]any{"adapterID": "luapure"})
	cl.waitEvent("initialized")

	cl.req("setBreakpoints", map[string]any{
		"source":      map[string]any{"name": "loop"},
		"breakpoints": []any{map[string]any{"line": 7}},
	})
	cl.req("launch", map[string]any{"program": "loop"})
	cl.req("configurationDone", nil)

	seen := []string{}
	for iter := 0; ; iter++ {
		// Either a stop (loop iteration) or termination.
		select {
		case e := <-cl.evt:
			switch e["event"] {
			case "stopped":
				// inspect this stop
				inspectStop(t, cl, &seen)
				cl.req("continue", nil)
			case "terminated":
				goto done
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no event")
		}
		if iter > 10 {
			t.Fatal("too many iterations")
		}
	}
done:
	cl.waitEvent("exited")
	if len(seen) != 3 || seen[0] != "1" || seen[2] != "3" {
		t.Fatalf("loop counters seen via evaluate = %v, want [1 2 3]", seen)
	}
}

func inspectStop(t *testing.T, cl *dapClient, seen *[]string) {
	t.Helper()
	cl.req("threads", nil)

	st := cl.req("stackTrace", nil)
	frames, _ := st["stackFrames"].([]any)
	if len(frames) == 0 {
		t.Fatal("empty stackTrace")
	}
	top := frames[0].(map[string]any)
	if top["line"].(float64) != 7 {
		t.Fatalf("top frame line = %v, want 7", top["line"])
	}
	// Source object carries a sourceReference (client has no local source).
	src, _ := top["source"].(map[string]any)
	if src == nil || src["sourceReference"] == nil {
		t.Fatalf("top frame missing source/sourceReference: %v", top)
	}
	// Fetch the source text over DAP.
	body := cl.req("source", map[string]any{"sourceReference": src["sourceReference"]})
	if body["content"] == "" {
		t.Error("empty source content")
	}

	// Scopes -> Locals -> variables.
	sc := cl.req("scopes", map[string]any{"frameId": top["id"]})
	scopes, _ := sc["scopes"].([]any)
	localsRef := scopes[0].(map[string]any)["variablesReference"]
	vars := cl.req("variables", map[string]any{"variablesReference": localsRef})
	varList, _ := vars["variables"].([]any)

	var cfgRef any
	haveI := false
	for _, v := range varList {
		m := v.(map[string]any)
		switch m["name"] {
		case "i":
			haveI = true
		case "cfg":
			cfgRef = m["variablesReference"]
		}
	}
	if !haveI {
		t.Errorf("locals missing 'i': %v", varList)
	}
	// Expand the cfg table (variable tree).
	if cfgRef == nil || cfgRef.(float64) == 0 {
		t.Fatalf("cfg should be expandable, ref = %v", cfgRef)
	}
	fields := cl.req("variables", map[string]any{"variablesReference": cfgRef})
	fl, _ := fields["variables"].([]any)
	got := map[string]string{}
	for _, f := range fl {
		m := f.(map[string]any)
		got[m["name"].(string)] = m["value"].(string)
	}
	if got["x"] != "10" || got["y"] != "20" {
		t.Errorf("cfg fields = %v, want x=10 y=20", got)
	}

	// Evaluate the loop counter in the stopped frame.
	ev := cl.req("evaluate", map[string]any{"expression": "i", "frameId": top["id"]})
	*seen = append(*seen, ev["result"].(string))
}
