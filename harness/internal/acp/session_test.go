package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestExtractSessionIDFromNotifications(t *testing.T) {
	tests := []struct {
		name   string
		notifs []*RPCResponse
		want   string
	}{
		{
			name:   "nil notifications",
			notifs: nil,
			want:   "",
		},
		{
			name: "session/update with sessionId",
			notifs: []*RPCResponse{
				{Method: "session/update", Params: json.RawMessage(`{"sessionId":"abc-123"}`)},
			},
			want: "abc-123",
		},
		{
			name: "ignores non-session/update",
			notifs: []*RPCResponse{
				{Method: "other/method", Params: json.RawMessage(`{"sessionId":"should-skip"}`)},
			},
			want: "",
		},
		{
			name: "returns first match",
			notifs: []*RPCResponse{
				{Method: "session/update", Params: json.RawMessage(`{"sessionId":"first"}`)},
				{Method: "session/update", Params: json.RawMessage(`{"sessionId":"second"}`)},
			},
			want: "first",
		},
		{
			name: "skips empty sessionId",
			notifs: []*RPCResponse{
				{Method: "session/update", Params: json.RawMessage(`{"sessionId":""}`)},
				{Method: "session/update", Params: json.RawMessage(`{"sessionId":"real-id"}`)},
			},
			want: "real-id",
		},
		{
			name: "handles malformed JSON",
			notifs: []*RPCResponse{
				{Method: "session/update", Params: json.RawMessage(`not-json`)},
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSessionIDFromNotifications(tt.notifs)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsToolCall(t *testing.T) {
	tests := []struct {
		name string
		msg  *RPCResponse
		want bool
	}{
		{
			name: "tool_call update",
			msg: &RPCResponse{
				Method: "session/update",
				Params: json.RawMessage(`{"update":{"sessionUpdate":"tool_call"}}`),
			},
			want: true,
		},
		{
			name: "agent_message_chunk is not a tool call",
			msg: &RPCResponse{
				Method: "session/update",
				Params: json.RawMessage(`{"update":{"sessionUpdate":"agent_message_chunk"}}`),
			},
			want: false,
		},
		{
			name: "wrong method",
			msg: &RPCResponse{
				Method: "other/method",
				Params: json.RawMessage(`{"update":{"sessionUpdate":"tool_call"}}`),
			},
			want: false,
		},
		{
			name: "malformed JSON",
			msg: &RPCResponse{
				Method: "session/update",
				Params: json.RawMessage(`garbage`),
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isToolCall(tt.msg)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSendPromptAnswersStringIDRequest proves agent-initiated requests
// with STRING ids are parsed, answered, and answered with the id echoed
// byte-exactly. Real goose allocates string ids (UUIDs) for its
// agent→client requests — session/request_permission arrives as
// {"id":"<uuid>", ...}. With ids parsed into *int64 the frame failed to
// unmarshal and was dropped; goose parks the turn on the missing reply
// with no timeout, hanging the stage until the pod deadline (observed
// live the first time a run entered approve mode).
func TestSendPromptAnswersStringIDRequest(t *testing.T) {
	replies := make(chan map[string]any, 1)

	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		// Read the client's session/prompt request.
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read prompt: %v", err)
			return
		}
		var promptReq struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(data, &promptReq); err != nil {
			t.Errorf("parse prompt: %v", err)
			return
		}

		// Agent-initiated permission request with a string id, as goose
		// actually sends it.
		perm := `{"jsonrpc":"2.0","id":"e0fcae7c-perm-1","method":"session/request_permission","params":{` +
			`"sessionId":"s1","toolCall":{"title":"shell · ls"},` +
			`"options":[{"optionId":"opt-ro","kind":"reject_once"}]}}`
		if err := conn.WriteMessage(websocket.TextMessage, []byte(perm)); err != nil {
			t.Errorf("send permission request: %v", err)
			return
		}
		_, data, err = conn.ReadMessage()
		if err != nil {
			t.Errorf("read permission reply: %v", err)
			return
		}
		var permReply map[string]any
		_ = json.Unmarshal(data, &permReply)
		replies <- permReply

		// Finish the turn.
		done := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"stopReason":"end_turn"}}`, promptReq.ID)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(done)); err != nil {
			t.Errorf("send prompt result: %v", err)
		}
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}
	port, _ := strconv.Atoi(u.Port())
	client, err := NewWSClient(u.Hostname(), port, "test-key")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sc := NewSessionClient(client)
	if _, err := sc.SendPrompt(ctx, "s1", []ContentBlock{{Type: "text", Text: "go"}}, 0); err != nil {
		t.Fatalf("prompt: %v", err)
	}

	reply := <-replies
	if got, _ := reply["id"].(string); got != "e0fcae7c-perm-1" {
		t.Fatalf("string id not echoed byte-exactly: %v", reply["id"])
	}
	result, _ := reply["result"].(map[string]any)
	outcome, _ := result["outcome"].(map[string]any)
	if outcome["outcome"] != "selected" || outcome["optionId"] != "opt-ro" {
		t.Fatalf("expected fail-closed reject_once, got %v", result)
	}
}

// Initialize must declare goose's customNotifications capability under the
// spec field name clientCapabilities._meta.goose — that switch turns on the
// `_goose/unstable/session/update` stream (usage_update, status_message)
// the tee forwards to viewers. The earlier "capabilities" field name was
// never read by goose at all.
func TestInitializeDeclaresGooseCustomNotifications(t *testing.T) {
	requests := make(chan map[string]any, 1)

	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read initialize: %v", err)
			return
		}
		var req map[string]any
		_ = json.Unmarshal(data, &req)
		requests <- req

		rawID, ok := req["id"].(float64)
		if !ok {
			t.Errorf("initialize request carried no numeric id: %v", req)
			return
		}
		resp := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":1,"agentCapabilities":{}}}`, int64(rawID))
		if err := conn.WriteMessage(websocket.TextMessage, []byte(resp)); err != nil {
			t.Errorf("send initialize result: %v", err)
			return
		}

		// Hold the connection open until the client hangs up. Returning
		// here would close the socket with the response still in flight,
		// and a close on a socket with unread data can RST — discarding
		// the response, which the client reports as "websocket connection
		// closed". Loopback usually wins that race; a loaded CI runner
		// does not.
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}
	port, _ := strconv.Atoi(u.Port())
	client, err := NewWSClient(u.Hostname(), port, "test-key")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sc := NewSessionClient(client)
	if _, err := sc.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	req := <-requests
	params, _ := req["params"].(map[string]any)
	caps, _ := params["clientCapabilities"].(map[string]any)
	meta, _ := caps["_meta"].(map[string]any)
	goose, _ := meta["goose"].(map[string]any)
	if goose["customNotifications"] != true {
		t.Fatalf("initialize params missing clientCapabilities._meta.goose.customNotifications=true: %v", params)
	}
}

type stubForwarder struct {
	result  json.RawMessage
	outcome PermissionForwardOutcome
	asked   chan json.RawMessage
}

func (f *stubForwarder) ForwardPermission(params json.RawMessage) (json.RawMessage, PermissionForwardOutcome) {
	if f.asked != nil {
		f.asked <- params
	}
	return f.result, f.outcome
}

const permAsk = `{"jsonrpc":"2.0","id":900,"method":"session/request_permission","params":{` +
	`"sessionId":"s1","toolCall":{"title":"edit pom.xml"},` +
	`"options":[{"optionId":"opt-aa","kind":"allow_always"},` +
	`{"optionId":"opt-ao","kind":"allow_once"},` +
	`{"optionId":"opt-ro","kind":"reject_once"},` +
	`{"optionId":"opt-ra","kind":"reject_always"}]}}`

// runPermissionScenario drives one prompt during which goose asks for
// permission, and returns the harness's reply to that ask.
func runPermissionScenario(t *testing.T, fwd PermissionForwarder) map[string]any {
	t.Helper()
	s := newDemuxServer(t)
	c := s.dial(t)
	sc := NewSessionClient(c)
	if fwd != nil {
		sc.SetPermissionForwarder(fwd)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	promptDone := make(chan error, 1)
	go func() {
		_, err := sc.SendPrompt(ctx, "s1", []ContentBlock{{Type: "text", Text: "go"}}, 0)
		promptDone <- err
	}()

	promptReq := s.next()
	promptID := int64(promptReq["id"].(float64))

	s.push(permAsk)
	reply := s.next()

	s.push(`{"jsonrpc":"2.0","id":` + jsonInt(promptID) + `,"result":{"stopReason":"end_turn"}}`)
	if err := <-promptDone; err != nil {
		t.Fatalf("prompt: %v", err)
	}

	if got := reply["id"].(float64); int64(got) != 900 {
		t.Fatalf("reply to id %v, want 900", got)
	}
	return reply
}

func jsonInt(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func selectedOption(t *testing.T, reply map[string]any) (string, string) {
	t.Helper()
	result, ok := reply["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in reply %v", reply)
	}
	outcome, ok := result["outcome"].(map[string]any)
	if !ok {
		t.Fatalf("no outcome in result %v", result)
	}
	kind, _ := outcome["outcome"].(string)
	opt, _ := outcome["optionId"].(string)
	return kind, opt
}

// A viewer's answer is relayed verbatim.
func TestPermissionForwardAnswered(t *testing.T) {
	fwd := &stubForwarder{
		result:  json.RawMessage(`{"outcome":{"outcome":"selected","optionId":"opt-aa"}}`),
		outcome: ForwardAnswered,
		asked:   make(chan json.RawMessage, 1),
	}
	reply := runPermissionScenario(t, fwd)

	kind, opt := selectedOption(t, reply)
	if kind != "selected" || opt != "opt-aa" {
		t.Fatalf("viewer answer not relayed verbatim: %s/%s", kind, opt)
	}

	params := <-fwd.asked
	var p struct {
		ToolCall struct {
			Title string `json:"title"`
		} `json:"toolCall"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.ToolCall.Title != "edit pom.xml" {
		t.Fatalf("forwarder saw wrong params: %s (%v)", params, err)
	}
}

// A viewer that walks away mid-ask gets the same fail-closed deny as
// nobody being attached — an ask that self-approves on a timer is no ask.
// (Retry burn is capped by the forwarder's unresponsive gate, not by
// approving unattended actions.)
func TestPermissionForwardTimeoutDenies(t *testing.T) {
	reply := runPermissionScenario(t, &stubForwarder{outcome: ForwardTimeout})
	kind, opt := selectedOption(t, reply)
	if kind != "selected" || opt != "opt-ro" {
		t.Fatalf("timeout should deny fail-closed, got %s/%s", kind, opt)
	}
}

// Nobody attached keeps the headless fail-closed deny.
func TestPermissionForwardNoViewersDenies(t *testing.T) {
	reply := runPermissionScenario(t, &stubForwarder{outcome: ForwardNoViewers})
	kind, opt := selectedOption(t, reply)
	if kind != "selected" || opt != "opt-ro" {
		t.Fatalf("no-viewers should deny, got %s/%s", kind, opt)
	}
}

// No forwarder at all (tee off) behaves identically.
func TestPermissionNoForwarderDenies(t *testing.T) {
	reply := runPermissionScenario(t, nil)
	kind, opt := selectedOption(t, reply)
	if kind != "selected" || opt != "opt-ro" {
		t.Fatalf("headless deny expected, got %s/%s", kind, opt)
	}
}

// Real goose allocates STRING ids for agent→client requests. The frame
// must parse, be answered, and the reply must echo the string id exactly —
// with *int64 ids this frame was dropped at unmarshal and goose parked the
// turn forever (seen live on dylan-mta, 2026-08-03).
func TestPermissionStringIDAnswered(t *testing.T) {
	s := newDemuxServer(t)
	c := s.dial(t)
	sc := NewSessionClient(c)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	promptDone := make(chan error, 1)
	go func() {
		_, err := sc.SendPrompt(ctx, "s1", []ContentBlock{{Type: "text", Text: "go"}}, 0)
		promptDone <- err
	}()

	promptReq := s.next()
	promptID := int64(promptReq["id"].(float64))

	ask := `{"jsonrpc":"2.0","id":"e0fcae7c-perm-1","method":"session/request_permission","params":{` +
		`"sessionId":"s1","toolCall":{"title":"shell · ls"},` +
		`"options":[{"optionId":"opt-ro","kind":"reject_once"}]}}`
	s.push(ask)
	reply := s.next()

	s.push(`{"jsonrpc":"2.0","id":` + jsonInt(promptID) + `,"result":{"stopReason":"end_turn"}}`)
	if err := <-promptDone; err != nil {
		t.Fatalf("prompt: %v", err)
	}

	if got, _ := reply["id"].(string); got != "e0fcae7c-perm-1" {
		t.Fatalf("string id not echoed: %v", reply["id"])
	}
	kind, opt := selectedOption(t, reply)
	if kind != "selected" || opt != "opt-ro" {
		t.Fatalf("string-id ask not denied properly: %s/%s", kind, opt)
	}
}
