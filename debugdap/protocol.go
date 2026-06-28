// Package debugdap exposes a luapure debug Session over the Debug Adapter
// Protocol (DAP), the protocol Visual Studio Code and other editors speak. A
// client attaches over TCP and drives breakpoints, stepping, stack/variable
// inspection and expression evaluation; the debuggee's source is served on
// demand, so the client needs no local copy. Like the rest of luapure it has no
// external dependencies — only the standard library and the luapure package.
//
// DAP is not JSON-RPC: messages are length-prefixed (Content-Length headers,
// like LSP) and come in three kinds — request, response, event — and control is
// asynchronous (a continue request returns immediately; the matching stop
// arrives later as a 'stopped' event). This package implements that wire format
// directly (protocol.go) and an Adapter that maps it onto a luapure Session.
package debugdap

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// request is an inbound DAP message. Clients send requests; this adapter issues
// no reverse requests, so responses/events only flow outward.
type request struct {
	Seq       int             `json:"seq"`
	Type      string          `json:"type"`
	Command   string          `json:"command"`
	Arguments json.RawMessage `json:"arguments"`
}

// codec reads and writes DAP messages over a stream, framing each with a
// Content-Length header. It is safe for one reader and concurrent writers
// (responses from the request loop, events from the pump goroutine).
type codec struct {
	r   *bufio.Reader
	wmu sync.Mutex
	w   io.Writer

	smu sync.Mutex
	seq int
}

func newCodec(r io.Reader, w io.Writer) *codec {
	return &codec{r: bufio.NewReader(r), w: w, seq: 1}
}

// readFrame returns the raw JSON body of the next Content-Length framed message.
func (c *codec) readFrame() ([]byte, error) {
	contentLength := -1
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // blank line ends the headers
		}
		if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("debugdap: bad Content-Length %q", v)
			}
			contentLength = n
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("debugdap: message without Content-Length")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(c.r, body); err != nil {
		return nil, err
	}
	return body, nil
}

// read returns the next inbound request, or an error (io.EOF when the peer
// closes).
func (c *codec) read() (*request, error) {
	body, err := c.readFrame()
	if err != nil {
		return nil, err
	}
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("debugdap: bad message body: %w", err)
	}
	return &req, nil
}

// send assigns a sequence number, marshals the message and writes it with its
// Content-Length header. msg is a response or event map (see respond/event).
func (c *codec) send(msg map[string]any) error {
	c.smu.Lock()
	msg["seq"] = c.seq
	c.seq++
	c.smu.Unlock()

	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(b)); err != nil {
		return err
	}
	_, err = c.w.Write(b)
	return err
}

// --- message builders ---

func respond(req *request, body any) map[string]any {
	return map[string]any{
		"type": "response", "request_seq": req.Seq, "success": true,
		"command": req.Command, "body": body,
	}
}

func respondErr(req *request, msg string) map[string]any {
	return map[string]any{
		"type": "response", "request_seq": req.Seq, "success": false,
		"command": req.Command, "message": msg,
	}
}

func event(name string, body any) map[string]any {
	return map[string]any{"type": "event", "event": name, "body": body}
}
