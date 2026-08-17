// go test -v -tags=integration -run TestSteerRedirect ./internal/acp/acptest/
// Requires goose >= 1.39 on PATH and a configured LLM provider.
//
// Live proof of the tee's redirect path: a viewer attached to the tee
// watches the run session's stream, learns the active run id from goose's
// session_info_update, injects a steer mid-turn through the tee, and the
// running agent's final answer reflects the redirect.

//go:build integration

package acptest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/konveyor/migration-harness/internal/acp"
	"github.com/konveyor/migration-harness/internal/goose"
	"github.com/konveyor/migration-harness/internal/tee"
)

const steerMarker = "PINEAPPLE"

func TestSteerRedirectsLiveRun(t *testing.T) {
	t.Setenv("GOOSE_MODE", "auto")
	// Isolate goose's session store from any older local goose install
	// (schema drift panics session/new); config + provider creds still
	// come from HOME.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("GOOSE_DISABLE_KEYRING", "1")

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	// goose loopback behind the tee, exactly as in the pod.
	srv, err := goose.StartServe(ctx, goose.ServeConfig{
		Port:         goose.LoopbackACPPort,
		BindLoopback: true,
		SecretKey:    "steer-probe-key",
	})
	if err != nil {
		t.Fatalf("StartServe: %v", err)
	}
	defer srv.Stop()

	runConn, err := acp.WaitReadyDial(ctx, "127.0.0.1", srv.Port(), srv.SecretKey(), 30*time.Second)
	if err != nil {
		t.Fatalf("WaitReadyDial: %v", err)
	}
	defer runConn.Close()

	session := acp.NewSessionClient(runConn)
	if _, err := session.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	sessionID, err := session.CreateSession(ctx, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	teeSrv := tee.New(tee.Config{
		SecretKey:    srv.SecretKey(),
		UpstreamAddr: fmt.Sprintf("127.0.0.1:%d", srv.Port()),
		SteerEnabled: true,
	})
	if err := teeSrv.Start(0); err != nil {
		t.Fatalf("tee start: %v", err)
	}
	defer teeSrv.Stop()
	teeSrv.AttachRun(runConn, sessionID)
	session.SetPermissionForwarder(teeSrv)

	// The human: a plain WebSocket client on the tee, like the UI.
	viewerURL := fmt.Sprintf("ws://%s/acp?token=%s", teeSrv.Addr(), url.QueryEscape(srv.SecretKey()))
	viewer, _, err := websocket.DefaultDialer.Dial(viewerURL, nil)
	if err != nil {
		t.Fatalf("viewer dial: %v", err)
	}
	defer viewer.Close()

	frames := make(chan map[string]any, 512)
	go func() {
		for {
			_, data, err := viewer.ReadMessage()
			if err != nil {
				close(frames)
				return
			}
			var f map[string]any
			if json.Unmarshal(data, &f) == nil {
				frames <- f
			}
		}
	}()

	// A prompt with a long enough tool phase to redirect mid-turn.
	teeSrv.SetRunActive(true)
	promptDone := make(chan error, 1)
	go func() {
		result, err := session.SendPrompt(ctx, sessionID, []acp.ContentBlock{{
			Type: "text",
			Text: "Use the shell to run `sleep 4` three separate times, one tool call " +
				"each, and after the third finish with exactly one short sentence " +
				"summarizing what you ran.",
		}}, 0)
		teeSrv.SetRunActive(false)
		if err == nil && result != nil {
			t.Logf("stopReason=%s chunks=%d", result.StopReason, len(result.Chunks))
		}
		promptDone <- err
	}()

	// Watch the teed stream for the active run id, steer, then verify the
	// pickup and the redirected final answer.
	var (
		runID      string
		steerSent  bool
		steerMsgID string
		sawPickup  bool
		sawUsage   bool
		endedEarly bool
		agentText  strings.Builder
		deadline   = time.After(3 * time.Minute)
		promptCh   = promptDone
	)
	for runID == "" || !steerSent || !sawPickup {
		select {
		case err := <-promptCh:
			if err != nil {
				t.Fatalf("prompt failed before steer completed: %v", err)
			}
			// Turn ended; drain what's buffered and stop watching.
			endedEarly = true
			promptCh = nil
			deadline = time.After(2 * time.Second)
		case f, ok := <-frames:
			if !ok {
				t.Fatal("viewer socket closed early")
			}
			switch f["method"] {
			case "session/update", "_goose/unstable/session/update":
			default:
				// Steer response rides back under the viewer's own id.
				if f["id"] == "human-steer-1" {
					result, _ := f["result"].(map[string]any)
					if result == nil {
						t.Fatalf("steer answered with error: %v", f)
					}
					steerMsgID, _ = result["messageId"].(string)
					t.Logf("steer accepted: runId=%v messageId=%s", result["runId"], steerMsgID)
				}
				continue
			}
			params, _ := f["params"].(map[string]any)
			update, _ := params["update"].(map[string]any)
			if update == nil {
				continue
			}
			kind, _ := update["sessionUpdate"].(string)
			meta, _ := update["_meta"].(map[string]any)
			gooseMeta, _ := meta["goose"].(map[string]any)

			if kind == "usage_update" {
				sawUsage = true
			}
			if id, _ := gooseMeta["activeRunId"].(string); id != "" && runID == "" {
				runID = id
				t.Logf("active run id: %s", runID)
			}
			if kind == "agent_message_chunk" {
				if content, _ := update["content"].(map[string]any); content != nil {
					text, _ := content["text"].(string)
					agentText.WriteString(text)
				}
			}
			// The steered message replays into the stream as a user
			// message flagged _meta.goose.steer.
			if kind == "user_message_chunk" && gooseMeta["steer"] == true {
				sawPickup = true
				t.Log("steer picked up by the running turn")
			}

			// First tool activity + known run id → inject the redirect.
			if runID != "" && !steerSent && (kind == "tool_call" || kind == "tool_call_update") {
				steer := map[string]any{
					"jsonrpc": "2.0",
					"id":      "human-steer-1",
					"method":  "_goose/unstable/session/steer",
					"params": map[string]any{
						"sessionId":     sessionID,
						"expectedRunId": runID,
						"prompt": []map[string]any{{
							"type": "text",
							"text": "Redirect from your human operator: skip any remaining sleeps if you can, and your final sentence MUST contain the word " + steerMarker + ".",
						}},
					},
				}
				if err := viewer.WriteJSON(steer); err != nil {
					t.Fatalf("steer write: %v", err)
				}
				steerSent = true
				t.Log("steer sent through the tee")
			}
		case <-deadline:
			t.Fatalf("timed out: runID=%q steerSent=%v pickup=%v", runID, steerSent, sawPickup)
		}
	}

	if !endedEarly {
		if err := <-promptDone; err != nil {
			t.Fatalf("prompt: %v", err)
		}
	}

	// Give the tail of the stream a moment, then judge the redirect.
	flush := time.After(2 * time.Second)
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				flush = time.After(0)
				break
			}
			params, _ := f["params"].(map[string]any)
			update, _ := params["update"].(map[string]any)
			switch kind, _ := update["sessionUpdate"].(string); kind {
			case "agent_message_chunk":
				if content, _ := update["content"].(map[string]any); content != nil {
					text, _ := content["text"].(string)
					agentText.WriteString(text)
				}
			case "usage_update":
				sawUsage = true
			}
			continue
		case <-flush:
		}
		break
	}

	final := agentText.String()
	t.Logf("agent transcript (%d chars): %s", len(final), final)
	if !strings.Contains(strings.ToUpper(final), steerMarker) {
		t.Fatalf("final answer does not reflect the steer (%q missing)", steerMarker)
	}
	if steerMsgID == "" {
		t.Fatal("steer response never returned a messageId")
	}
	if sawUsage {
		t.Log("usage_update frames observed on the viewer stream")
	} else {
		t.Log("note: no usage_update frames seen on the viewer stream")
	}
}
