package acp

import (
	"context"
	"encoding/json"
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

// TestSendPromptAnswersAgentRequests proves the prompt loop answers
// agent-initiated requests instead of dropping them. goose parks the turn
// on these replies with no timeout (and session/cancel cannot unpark it),
// so an unanswered request hangs the stage until the pod deadline. Without
// the answering branch this test times out.
func TestSendPromptAnswersAgentRequests(t *testing.T) {
	type reply struct {
		method string
		body   map[string]any
	}
	replies := make(chan reply, 3)

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

		// Agent-initiated permission request mid-turn.
		perm := `{"jsonrpc":"2.0","id":900,"method":"session/request_permission","params":{` +
			`"sessionId":"s1","toolCall":{"title":"shell · rm -rf"},` +
			`"options":[{"optionId":"allow_always","kind":"allow_always"},` +
			`{"optionId":"allow_once","kind":"allow_once"},` +
			`{"optionId":"reject_once","kind":"reject_once"},` +
			`{"optionId":"reject_always","kind":"reject_always"}]}}`
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
		replies <- reply{method: "session/request_permission", body: permReply}

		// The agent asks the human a question; headless, it is cancelled
		// (never answered on the human's behalf).
		elic := `{"jsonrpc":"2.0","id":901,"method":"elicitation/create","params":{` +
			`"message":"Which broker?","requestedSchema":{"type":"object"}}}`
		if err := conn.WriteMessage(websocket.TextMessage, []byte(elic)); err != nil {
			t.Errorf("send elicitation request: %v", err)
			return
		}
		_, data, err = conn.ReadMessage()
		if err != nil {
			t.Errorf("read elicitation reply: %v", err)
			return
		}
		var elicReply map[string]any
		_ = json.Unmarshal(data, &elicReply)
		replies <- reply{method: "elicitation/create", body: elicReply}

		// Agent-initiated request the harness does not implement.
		fsReq := `{"jsonrpc":"2.0","id":902,"method":"fs/read_text_file","params":{"path":"/etc/passwd"}}`
		if err := conn.WriteMessage(websocket.TextMessage, []byte(fsReq)); err != nil {
			t.Errorf("send fs request: %v", err)
			return
		}
		_, data, err = conn.ReadMessage()
		if err != nil {
			t.Errorf("read fs reply: %v", err)
			return
		}
		var fsReply map[string]any
		_ = json.Unmarshal(data, &fsReply)
		replies <- reply{method: "fs/read_text_file", body: fsReply}

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
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse test server port: %v", err)
	}

	ws, err := NewWSClient(u.Hostname(), port, "test-secret")
	if err != nil {
		t.Fatalf("NewWSClient: %v", err)
	}
	defer ws.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := NewSessionClient(ws)
	result, err := client.SendPrompt(ctx, "s1", []ContentBlock{{Type: "text", Text: "go"}}, 0)
	if err != nil {
		t.Fatalf("SendPrompt: %v (a timeout here means an agent request went unanswered)", err)
	}
	if result.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want end_turn", result.StopReason)
	}

	// Permission request must be answered with the reject_once option.
	r := <-replies
	if id, _ := r.body["id"].(float64); id != 900 {
		t.Errorf("permission reply id = %v, want 900", r.body["id"])
	}
	if !strings.Contains(fmt.Sprintf("%v", r.body["result"]), "reject_once") {
		t.Errorf("permission reply should select reject_once, got: %v", r.body["result"])
	}

	// A question with nobody to answer it is cancelled, never answered
	// for the human.
	r = <-replies
	if id, _ := r.body["id"].(float64); id != 901 {
		t.Errorf("elicitation reply id = %v, want 901", r.body["id"])
	}
	if !strings.Contains(fmt.Sprintf("%v", r.body["result"]), "cancel") {
		t.Errorf("headless elicitation reply should cancel, got: %v", r.body)
	}

	// Unsupported request must get a method-not-found error, not silence.
	r = <-replies
	if id, _ := r.body["id"].(float64); id != 902 {
		t.Errorf("fs reply id = %v, want 902", r.body["id"])
	}
	if r.body["error"] == nil {
		t.Errorf("fs reply should be a JSON-RPC error, got: %v", r.body)
	}
}
