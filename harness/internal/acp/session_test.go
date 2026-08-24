package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
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
	// Form elicitation must be advertised or goose cancels the agent's
	// ask_user questions itself instead of relaying them.
	elicitation, _ := caps["elicitation"].(map[string]any)
	if _, ok := elicitation["form"]; !ok {
		t.Fatalf("initialize params missing clientCapabilities.elicitation.form: %v", params)
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

func (f *stubForwarder) ForwardElicitation(params json.RawMessage) (json.RawMessage, PermissionForwardOutcome) {
	if f.asked != nil {
		f.asked <- params
	}
	return f.result, f.outcome
}

const elicitAsk = `{"jsonrpc":"2.0","id":"elic-1","method":"elicitation/create","params":{` +
	`"sessionId":"s1","mode":"form","message":"Which database should the migration target?",` +
	`"requestedSchema":{"type":"object","properties":{"answer":{"type":"string","enum":["postgres","mysql"]}},"required":["answer"]}}}`

// runElicitationScenario drives one prompt during which the agent asks
// the human a question, and returns the harness's reply to that ask plus
// the session client (so callers can inspect the HITL-gate flag). The run
// session id is left unset, so the unanswered-path turn cancel is a no-op
// here — TestElicitationUnansweredCancelsTurn covers that frame separately.
func runElicitationScenario(t *testing.T, fwd PermissionForwarder) (map[string]any, *SessionClient) {
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

	s.push(elicitAsk)
	reply := s.next()

	s.push(`{"jsonrpc":"2.0","id":` + jsonInt(promptID) + `,"result":{"stopReason":"end_turn"}}`)
	if err := <-promptDone; err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if got, _ := reply["id"].(string); got != "elic-1" {
		t.Fatalf("reply to id %v, want elic-1", reply["id"])
	}
	return reply, sc
}

func elicitationAction(t *testing.T, reply map[string]any) (string, map[string]any) {
	t.Helper()
	result, ok := reply["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in reply %v", reply)
	}
	action, _ := result["action"].(string)
	content, _ := result["content"].(map[string]any)
	return action, content
}

// mcpServers rides goose's untagged-enum parse, proven only against
// args/env present as arrays — nil slices must never marshal as null.
func TestMCPServerMarshalNilSlicesAsEmptyArrays(t *testing.T) {
	b, err := json.Marshal(SessionNewParams{
		CWD:        "/w",
		MCPServers: []MCPServer{{Name: "ask", Command: "/bin/harness"}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"args":[]`, `"env":[]`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("wire shape lost %s: %s", want, b)
		}
	}
}

// The human's answer to a question is relayed verbatim, and an answered
// question is not a failed run.
func TestElicitationForwardAnswered(t *testing.T) {
	fwd := &stubForwarder{
		result:  json.RawMessage(`{"action":"accept","content":{"answer":"postgres"}}`),
		outcome: ForwardAnswered,
		asked:   make(chan json.RawMessage, 1),
	}
	reply, sc := runElicitationScenario(t, fwd)
	action, content := elicitationAction(t, reply)
	if action != "accept" || content["answer"] != "postgres" {
		t.Fatalf("viewer answer not relayed verbatim: %s %v", action, content)
	}
	if sc.HITLGateUnanswered() {
		t.Fatal("an answered question must not fail the run")
	}
	params := <-fwd.asked
	if !strings.Contains(string(params), "Which database") {
		t.Fatalf("forwarder saw wrong params: %s", params)
	}
}

// Nobody to answer — or nobody answering in time — cancels the question
// (the harness never answers on the human's behalf) AND fails the run: an
// ask_user gate the agent could not clear must not let the model steamroll
// a guess.
func TestElicitationFailsClosed(t *testing.T) {
	for name, fwd := range map[string]PermissionForwarder{
		"no viewers":   &stubForwarder{outcome: ForwardNoViewers},
		"timeout":      &stubForwarder{outcome: ForwardTimeout},
		"no forwarder": nil,
	} {
		t.Run(name, func(t *testing.T) {
			reply, sc := runElicitationScenario(t, fwd)
			action, _ := elicitationAction(t, reply)
			if action != "cancel" {
				t.Fatalf("expected cancel, got %q", action)
			}
			if !sc.HITLGateUnanswered() {
				t.Fatal("an unanswered ask_user question must fail the run")
			}
		})
	}
}

// An unanswered question stops the turn: after the cancel reply unparks the
// turn (the only key that can), the harness fires session/cancel on the run
// session so the model cannot proceed on the unanswered gate.
func TestElicitationUnansweredCancelsTurn(t *testing.T) {
	s := newDemuxServer(t)
	c := s.dial(t)
	sc := NewSessionClient(c)
	sc.SetPermissionForwarder(&stubForwarder{outcome: ForwardTimeout})
	// CreateSession is not run in this scripted scenario; set the run
	// session id directly so cancelTurn has a target.
	sc.sessionID.Store("s1")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	promptDone := make(chan error, 1)
	go func() {
		_, err := sc.SendPrompt(ctx, "s1", []ContentBlock{{Type: "text", Text: "go"}}, 0)
		promptDone <- err
	}()

	promptReq := s.next()
	promptID := int64(promptReq["id"].(float64))

	s.push(elicitAsk)

	// First the cancel reply to the ask (unparks the turn)...
	reply := s.next()
	if action, _ := elicitationAction(t, reply); action != "cancel" {
		t.Fatalf("expected cancel reply, got %q", action)
	}
	// ...then session/cancel for the run session (stops the turn).
	cancelFrame := s.next()
	if cancelFrame["method"] != "session/cancel" {
		t.Fatalf("expected session/cancel after unanswered ask, got %v", cancelFrame["method"])
	}
	params, _ := cancelFrame["params"].(map[string]any)
	if params["sessionId"] != "s1" {
		t.Fatalf("session/cancel targeted %v, want s1", params["sessionId"])
	}

	s.push(`{"jsonrpc":"2.0","id":` + jsonInt(promptID) + `,"result":{"stopReason":"cancelled"}}`)
	if err := <-promptDone; err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if !sc.HITLGateUnanswered() {
		t.Fatal("HITL gate flag not latched")
	}
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

// The prompt error the harness logs must carry goose's data field.
func TestSendPromptErrorIncludesGooseDetail(t *testing.T) {
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
		// Text the agent streamed before dying, then what goose 1.45 answers
		// when the session's provider never got built: message is
		// boilerplate, data has the reason.
		chunk := `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":` +
			`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Starting."}}}}`
		if err := conn.WriteMessage(websocket.TextMessage, []byte(chunk)); err != nil {
			t.Errorf("send chunk: %v", err)
			return
		}
		resp := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"error":{"code":-32603,"message":"Internal error",`+
			`"data":"Error getting agent reply: Provider not set"}}`, promptReq.ID)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(resp)); err != nil {
			t.Errorf("send error: %v", err)
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

	result, err := NewSessionClient(client).SendPrompt(ctx, "s1", []ContentBlock{{Type: "text", Text: "go"}}, 0)
	if err == nil {
		t.Fatal("expected an error")
	}
	want := "prompt error -32603: Internal error — Error getting agent reply: Provider not set"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32603 {
		t.Fatalf("error does not wrap the *RPCError: %v", err)
	}
	// The partial result survives the failure: it is where the closing
	// message is when the turn ends like this.
	if result == nil || result.FinalMessage() != "Starting." {
		t.Fatalf("partial result not returned with the error: %+v", result)
	}
}

func sessionUpdate(update string) *RPCResponse {
	return &RPCResponse{
		Method: "session/update",
		Params: json.RawMessage(`{"sessionId":"s1","update":` + update + `}`),
	}
}

func textChunk(text, messageID string) string {
	return `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":` + strconv.Quote(text) + `},` +
		`"_meta":{"goose":{"messageId":` + strconv.Quote(messageID) + `}}}`
}

// The closing message is the text after the last tool call: narration that
// introduces a call is not what the agent concluded with. Distinct goose
// messages stay distinct; a mid-turn steer starts the closing text over.
func TestPromptResultFinalMessageFollowsLastToolCall(t *testing.T) {
	result := &PromptResult{}
	for _, u := range []string{
		textChunk("Let me look at the repository first.", "m1"),
		`{"sessionUpdate":"tool_call","toolCallId":"c1","title":"shell · ls","status":"pending"}`,
		`{"sessionUpdate":"tool_call_update","toolCallId":"c1","status":"completed"}`,
		textChunk("**Round 1/10:** ", "m2"),
		textChunk("Tell me something.", "m2"),
		textChunk("I've reached the maximum number of actions I can do without user input.", "m3"),
	} {
		handlePromptNotification(sessionUpdate(u), result)
	}
	want := "**Round 1/10:** Tell me something.\nI've reached the maximum number of actions I can do without user input."
	if got := result.FinalMessage(); got != want {
		t.Fatalf("FinalMessage() = %q, want %q", got, want)
	}
	if len(result.Chunks) != 4 {
		t.Fatalf("Chunks = %d, want 4 (all text is still collected)", len(result.Chunks))
	}

	// No tool call at all: the whole reply is the closing message.
	result = &PromptResult{}
	handlePromptNotification(sessionUpdate(textChunk("Tell me something.", "m1")), result)
	if got := result.FinalMessage(); got != "Tell me something." {
		t.Fatalf("FinalMessage() without tools = %q", got)
	}

	// A human steer mid-turn: the closing text is the reply to it.
	handlePromptNotification(sessionUpdate(
		`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"stop and summarize"},`+
			`"_meta":{"goose":{"steer":true}}}`), result)
	handlePromptNotification(sessionUpdate(textChunk("Summary: nothing changed.", "m2")), result)
	if got := result.FinalMessage(); got != "Summary: nothing changed." {
		t.Fatalf("FinalMessage() after steer = %q", got)
	}
}

// goose's own terminal notices travel on its custom update method, not as
// agent text; they must be kept with the result.
func TestPromptResultKeepsGooseNotices(t *testing.T) {
	result := &PromptResult{}
	notice := &RPCResponse{
		Method: gooseUpdateMethod,
		Params: json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"status_message",` +
			`"status":{"type":"notice","message":"Unable to continue: Context limit still exceeded after compaction."}}}`),
	}
	usage := &RPCResponse{
		Method: gooseUpdateMethod,
		Params: json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"usage_update","usage":{"totalTokens":5}}}`),
	}
	handlePromptNotification(usage, result)
	handlePromptNotification(notice, result)
	if len(result.Notices) != 1 || !strings.HasPrefix(result.Notices[0], "Unable to continue") {
		t.Fatalf("Notices = %q", result.Notices)
	}
}

func TestLooksLikeProviderError(t *testing.T) {
	yes := []string{
		"Ran into this error: Failed to call Bedrock: ValidationException.\n\nPlease retry if you think this is a transient or recoverable error.",
		"Ran into this error trying to compact: throttled.\n\nPlease try again or create a new session",
		"The provider refused this request.\n\nsafety\n\nPlease start a new session to continue",
		"Connection reset by peer\n\nPlease resend your message to try again.",
		"The model returned an empty response. Please resend your message to continue.",
	}
	for _, s := range yes {
		if !LooksLikeProviderError(s) {
			t.Errorf("expected provider error: %q", s)
		}
	}
	// Streamed text followed by the failure in the same entry (no message
	// id separated them): the failure starts a line.
	yes = append(yes, "Here is the plan:\n1. Update pom.xml\nRan into this error: ThrottlingException.\n\nPlease retry if you think this is a transient or recoverable error.")
	if !LooksLikeProviderError(yes[len(yes)-1]) {
		t.Errorf("expected provider error after streamed text: %q", yes[len(yes)-1])
	}
	no := []string{
		"Tell me something.",
		"Done — committed 3 files.",
		"The build log said \"Ran into this error: missing jakarta import\"; fixed in 2 files.",
		"",
	}
	for _, s := range no {
		if LooksLikeProviderError(s) {
			t.Errorf("false positive: %q", s)
		}
	}

	// Per message: a failure after an ordinary message is still a failure.
	r := &PromptResult{}
	r.appendClosing("Done with the first file.", "m1")
	r.appendClosing("Ran into this error: 503.\n\nPlease retry if you think this is a transient or recoverable error.", "m2")
	if !r.ClosingProviderError() {
		t.Fatal("provider error in a later message not detected")
	}
}
