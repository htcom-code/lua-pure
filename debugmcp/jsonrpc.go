package debugmcp

import "encoding/json"

// JSON-RPC 2.0 wire types (the subset MCP uses). A message with no ID is a
// notification and receives no response.

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return e.Message }

// Standard JSON-RPC 2.0 error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

func errf(code int, msg string) *rpcError { return &rpcError{Code: code, Message: msg} }

// encodeResult / encodeError build the response frame for a request id. Marshal
// cannot fail for these plain map/struct payloads, so the error is dropped.
func encodeResult(id json.RawMessage, result any) []byte {
	b, _ := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
	return b
}

func encodeError(id json.RawMessage, e *rpcError) []byte {
	if id == nil {
		id = json.RawMessage("null")
	}
	b, _ := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: id, Error: e})
	return b
}
