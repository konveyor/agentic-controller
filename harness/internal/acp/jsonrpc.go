package acp

import (
	"bytes"
	"encoding/json"
	"sync/atomic"
)

// JSON-RPC 2.0 types for ACP communication with goose serve.

type Request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// RPCResponse is any inbound frame: a response to one of our requests, a
// notification, or an agent-initiated request. The ID is kept as raw JSON
// because the two sides use different id spaces — our outbound requests
// use int64, but goose allocates STRING ids for its agent→client requests
// (session/request_permission arrives as {"id":"<uuid>", ...}); parsing
// into *int64 silently dropped those frames and parked the turn forever.
type RPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

var jsonNull = []byte("null")

// HasID reports whether the frame carries a non-null id.
func (r *RPCResponse) HasID() bool {
	return len(r.ID) > 0 && !bytes.Equal(r.ID, jsonNull)
}

// IntID returns the id as int64 when it is numeric — the only shape our
// own outbound requests use, so response routing keys on it.
func (r *RPCResponse) IntID() (int64, bool) {
	if !r.HasID() {
		return 0, false
	}
	var n int64
	if err := json.Unmarshal(r.ID, &n); err != nil {
		return 0, false
	}
	return n, true
}

func (r *RPCResponse) IsNotification() bool {
	return !r.HasID() && r.Method != ""
}

// IsAgentRequest reports whether the message is a request initiated by the
// agent that the client must answer (e.g. session/request_permission,
// elicitation/create). Unlike a notification it carries an ID (string or
// number), and unlike a response to one of our own calls it carries a
// method.
func (r *RPCResponse) IsAgentRequest() bool {
	return r.HasID() && r.Method != ""
}

// Response is an outgoing JSON-RPC response to an agent-initiated request.
// The ID echoes the request's id bytes exactly — replying to a string-id
// request with a numeric id would never match and goose would stay parked.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return e.Message
}

var requestID atomic.Int64

func newRequest(method string, params any) *Request {
	return &Request{
		JSONRPC: "2.0",
		ID:      requestID.Add(1),
		Method:  method,
		Params:  params,
	}
}
