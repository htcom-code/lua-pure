package debugmcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	luapure "github.com/htcom-code/lua-pure/lua"
)

func testNewState() *luapure.LState {
	L := luapure.NewState()
	L.OpenLibs()
	return L
}

// callDirect invokes a tool via HandleMessage (as the HTTP handler does) and
// returns its structuredContent payload.
func callDirect(t *testing.T, s *Server, id int, name string, args map[string]any) map[string]any {
	t.Helper()
	req := map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	}
	b, _ := json.Marshal(req)
	reply := s.HandleMessage(b)
	var r struct {
		Result map[string]any `json:"result"`
		Error  *rpcError      `json:"error"`
	}
	if err := json.Unmarshal(reply, &r); err != nil {
		t.Fatalf("bad reply: %v (%s)", err, reply)
	}
	if r.Error != nil {
		t.Fatalf("%s protocol error: %s", name, r.Error.Message)
	}
	sc, _ := r.Result["structuredContent"].(map[string]any)
	return sc
}

// Pause must interrupt a control tool that is blocked waiting for the next stop
// — the concurrency the HTTP transport relies on (HandleMessage is called per
// request, concurrently). The serial Serve loop cannot exercise this, so the
// test calls HandleMessage directly from two goroutines.
func TestConcurrentPauseDuringRun(t *testing.T) {
	db := map[string]string{"inf": "while true do end\n"}
	s := &Server{
		NewState: testNewState,
		Source:   func(id string) (string, bool) { v, ok := db[id]; return v, ok },
	}

	launched := make(chan map[string]any, 1)
	go func() {
		// No breakpoint: launch runs the infinite loop and blocks until paused.
		launched <- callDirect(t, s, 1, "launch", map[string]any{"program": "inf"})
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		callDirect(t, s, 2, "pause", nil) // concurrent with the blocked launch
		select {
		case ev := <-launched:
			if ev["event"] != "stopped" {
				t.Fatalf("launch returned %v, want a stopped event", ev)
			}
			if ev["reason"] != "pause" {
				t.Fatalf("stop reason = %v, want pause", ev["reason"])
			}
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("pause did not interrupt the running program")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The HTTP handler drives a session over POSTs: initialize, then launch a
// program that runs to completion, persisting state across requests on one
// Server (one session).
func TestHTTPHandler(t *testing.T) {
	db := map[string]string{"p": "local x = 21\nreturn x * 2\n"}
	s := &Server{
		NewState: testNewState,
		Source:   func(id string) (string, bool) { v, ok := db[id]; return v, ok },
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	post := func(id int, method string, params any) map[string]any {
		t.Helper()
		req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
		if params != nil {
			req["params"] = params
		}
		b, _ := json.Marshal(req)
		resp, err := http.Post(ts.URL, "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s: status %d", method, resp.StatusCode)
		}
		var r struct {
			Result map[string]any `json:"result"`
			Error  *rpcError      `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&r)
		if r.Error != nil {
			t.Fatalf("%s error: %s", method, r.Error.Message)
		}
		return r.Result
	}

	init := post(1, "initialize", map[string]any{"protocolVersion": latestProtocol})
	if init["protocolVersion"] != latestProtocol {
		t.Errorf("protocolVersion = %v", init["protocolVersion"])
	}

	res := post(2, "tools/call", map[string]any{"name": "launch", "arguments": map[string]any{"program": "p"}})
	sc, _ := res["structuredContent"].(map[string]any)
	if sc["event"] != "finished" {
		t.Fatalf("launch event = %v, want finished", sc["event"])
	}
	results, _ := sc["results"].([]any)
	if len(results) != 1 || results[0].(string) != "42" {
		t.Fatalf("results = %v, want [42]", results)
	}
}

// A bad MCP-Protocol-Version header is rejected.
func TestHTTPBadProtocolVersion(t *testing.T) {
	s := &Server{NewState: testNewState}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL, bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)))
	req.Header.Set("MCP-Protocol-Version", "1999-01-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
