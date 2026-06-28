package debugmcp

import (
	"io"
	"net/http"
)

// maxHTTPBody bounds a single request body, so a hostile client cannot exhaust
// memory with one giant message.
const maxHTTPBody = 4 << 20 // 4 MiB

// Handler returns an http.Handler implementing MCP's Streamable HTTP transport
// for this server: a POST carries one JSON-RPC message and the response body is
// the single JSON-RPC reply. There is no SSE stream — this debug server never
// pushes unsolicited messages (a control tool returns its stop synchronously),
// so plain JSON replies suffice.
//
// SECURITY. The handler performs no authentication, TLS, Origin validation or
// rate limiting, and it executes arbitrary Lua on the caller's behalf — exposing
// it unguarded is remote code execution. Mount it behind middleware that
// enforces auth and validates Origin (DNS-rebinding protection), bind to
// localhost unless the front is trusted, and prefer a sandboxed NewState for
// untrusted callers. One Server is one debug session; route each session (e.g.
// by its Mcp-Session-Id) to its own Server and Handler.
//
// Control tools (launch/continue/step_*) block until the next stop, so the POST
// is held open until then — configure generous server write/idle timeouts, and
// send pause as a separate concurrent request to interrupt a long run.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "only POST is supported", http.StatusMethodNotAllowed)
			return
		}
		// After initialize, MCP clients send the negotiated protocol version; if
		// present, it must be one we speak.
		if v := r.Header.Get("MCP-Protocol-Version"); v != "" && !supportedProtocols[v] {
			http.Error(w, "unsupported MCP-Protocol-Version: "+v, http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxHTTPBody))
		if err != nil {
			http.Error(w, "request body read error", http.StatusBadRequest)
			return
		}
		reply := s.HandleMessage(body)
		if reply == nil {
			w.WriteHeader(http.StatusAccepted) // a notification has no reply
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(reply)
	})
}
