// Package debugmcp exposes a luapure debug Session over the Model Context
// Protocol (MCP), so an LLM agent can drive breakpoints, stepping and
// expression evaluation through tool calls. It speaks MCP's JSON-RPC 2.0 wire
// format with no external dependencies — only the Go standard library and the
// luapure package.
//
// The protocol surface is intentionally small: initialize, tools/list and
// tools/call, plus a set of debug tools (see Server). Transport is abstracted
// (Transport), so the same Server runs over stdio today and HTTP later. The
// debuggee's source need not live with the client: source text is fetched on
// demand through a SourceResolver, mirroring the luapure Session design.
package debugmcp

import (
	"bufio"
	"io"
	"sync"
)

// Transport carries whole JSON-RPC messages as byte frames. Read returns the
// next inbound message; Write sends one; both are one message per call. An
// implementation must be safe for one reader and one writer used concurrently.
type Transport interface {
	Read() ([]byte, error) // next message frame; io.EOF when the peer closes
	Write(msg []byte) error
	Close() error
}

// stdioTransport is MCP's standard local transport: newline-delimited JSON-RPC
// messages on a reader/writer pair (typically os.Stdin/os.Stdout). Messages
// carry no embedded newlines, per the MCP stdio spec.
type stdioTransport struct {
	r  *bufio.Reader
	wm sync.Mutex
	w  io.Writer
	c  io.Closer
}

// NewStdioTransport builds a transport over r/w; closer (may be nil) is run by
// Close. For a process spawned by an MCP client, pass os.Stdin and os.Stdout.
func NewStdioTransport(r io.Reader, w io.Writer, closer io.Closer) Transport {
	return &stdioTransport{r: bufio.NewReader(r), w: w, c: closer}
}

func (t *stdioTransport) Read() ([]byte, error) {
	line, err := t.r.ReadBytes('\n')
	if err != nil {
		// Return any trailing bytes before the error (e.g. a final unterminated
		// message) so the caller can still process them.
		if len(line) > 0 && err == io.EOF {
			return trimNewline(line), nil
		}
		return nil, err
	}
	return trimNewline(line), nil
}

func (t *stdioTransport) Write(msg []byte) error {
	t.wm.Lock()
	defer t.wm.Unlock()
	if _, err := t.w.Write(msg); err != nil {
		return err
	}
	_, err := t.w.Write([]byte{'\n'})
	return err
}

func (t *stdioTransport) Close() error {
	if t.c != nil {
		return t.c.Close()
	}
	return nil
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
