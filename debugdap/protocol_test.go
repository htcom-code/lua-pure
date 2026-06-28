package debugdap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// read parses a Content-Length framed request.
func TestCodecRead(t *testing.T) {
	body := `{"seq":3,"type":"request","command":"initialize","arguments":{"adapterID":"x"}}`
	frame := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
	c := newCodec(strings.NewReader(frame), &bytes.Buffer{})

	req, err := c.read()
	if err != nil {
		t.Fatal(err)
	}
	if req.Seq != 3 || req.Type != "request" || req.Command != "initialize" {
		t.Fatalf("parsed %+v", req)
	}
	var args struct {
		AdapterID string `json:"adapterID"`
	}
	json.Unmarshal(req.Arguments, &args)
	if args.AdapterID != "x" {
		t.Errorf("arguments not parsed: %q", args.AdapterID)
	}
}

// send frames a message with a Content-Length header and an incrementing seq.
func TestCodecSend(t *testing.T) {
	var buf bytes.Buffer
	c := newCodec(strings.NewReader(""), &buf)

	if err := c.send(event("stopped", map[string]any{"reason": "breakpoint"})); err != nil {
		t.Fatal(err)
	}
	if err := c.send(respond(&request{Seq: 9, Command: "next"}, nil)); err != nil {
		t.Fatal(err)
	}

	// Parse the two frames back out.
	rc := newCodec(bytes.NewReader(buf.Bytes()), &bytes.Buffer{})
	first, err := readAny(rc)
	if err != nil {
		t.Fatal(err)
	}
	if first["seq"].(float64) != 1 || first["type"] != "event" || first["event"] != "stopped" {
		t.Fatalf("first frame = %v", first)
	}
	second, err := readAny(rc)
	if err != nil {
		t.Fatal(err)
	}
	if second["seq"].(float64) != 2 || second["type"] != "response" {
		t.Fatalf("second frame = %v", second)
	}
	if second["success"] != true || second["request_seq"].(float64) != 9 {
		t.Fatalf("response fields = %v", second)
	}
}

// readAny reads one frame and unmarshals it to a generic map.
func readAny(c *codec) (map[string]any, error) {
	body, err := c.readFrame()
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	return m, nil
}
