// Live probe of the in-turn "ask the human" loop: real goose (>= 1.45 on
// PATH, LLM provider configured) mounts the harness's ask_user MCP server,
// the model calls the tool, the question reaches a viewer through the tee
// as an elicitation/create ask, the viewer answers, and the model's final
// sentence reflects the answer. The turn must block on the answer: the
// prompt result may only arrive after the viewer replies.
//
//go:build integration

package acptest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/konveyor/migration-harness/internal/acp"
	"github.com/konveyor/migration-harness/internal/askuser"
	"github.com/konveyor/migration-harness/internal/goose"
	"github.com/konveyor/migration-harness/internal/tee"
)

const askMarker = "postgres"

func buildHarness(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "migration-harness")
	cmd := exec.Command("go", "build", "-o", bin, "../../../cmd/migration-harness")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build harness: %v", err)
	}
	return bin
}

func TestAskUserBlocksLiveRun(t *testing.T) {
	t.Setenv("GOOSE_MODE", "auto")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("GOOSE_DISABLE_KEYRING", "1")

	harnessBin := buildHarness(t)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	srv, err := goose.StartServe(ctx, goose.ServeConfig{
		Port:         goose.LoopbackACPPort,
		BindLoopback: true,
		SecretKey:    "ask-probe-key",
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
	// Exactly what runStage mounts: the harness binary as the ask_user
	// MCP server.
	sessionID, err := session.CreateSession(ctx, t.TempDir(), []acp.MCPServer{{
		Name:    askuser.ToolName,
		Command: harnessBin,
		Args:    []string{askuser.Subcommand},
		Env:     []acp.EnvVar{},
	}})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	teeSrv := tee.New(tee.Config{
		SecretKey:    srv.SecretKey(),
		UpstreamAddr: fmt.Sprintf("127.0.0.1:%d", srv.Port()),
		SteerEnabled: true,
		HITLTimeout:  3 * time.Minute,
	})
	if err := teeSrv.Start(0); err != nil {
		t.Fatalf("tee start: %v", err)
	}
	defer teeSrv.Stop()
	teeSrv.AttachRun(runConn, sessionID)
	session.SetPermissionForwarder(teeSrv)

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

	teeSrv.SetRunActive(true)
	promptDone := make(chan error, 1)
	promptEnded := make(chan time.Time, 1)
	go func() {
		result, err := session.SendPrompt(ctx, sessionID, []acp.ContentBlock{{
			Type: "text",
			Text: "You have an ask_user tool. Before doing anything else, call it to ask " +
				"which database the migration should target, offering exactly the options " +
				"postgres and mysql. Then finish with exactly one short sentence that names " +
				"the answer you received. Do not run any other tools.",
		}}, 0)
		promptEnded <- time.Now()
		teeSrv.SetRunActive(false)
		if err == nil && result != nil {
			t.Logf("stopReason=%s chunks=%d", result.StopReason, len(result.Chunks))
		}
		promptDone <- err
	}()

	// Wait for the question to reach the viewer as a kask-* ask.
	var askID string
	deadline := time.After(3 * time.Minute)
	for askID == "" {
		select {
		case err := <-promptDone:
			t.Fatalf("prompt ended (%v) before the agent asked the human anything", err)
		case f, ok := <-frames:
			if !ok {
				t.Fatal("viewer socket closed early")
			}
			if f["method"] != "elicitation/create" {
				continue
			}
			id, _ := f["id"].(string)
			if !strings.HasPrefix(id, "kask-") {
				t.Fatalf("ask under unexpected id: %v", f)
			}
			params, _ := f["params"].(map[string]any)
			schema, _ := params["requestedSchema"].(map[string]any)
			t.Logf("question reached the viewer as %s: %q schema=%v", id, params["message"], schema)
			if !strings.Contains(strings.ToLower(fmt.Sprint(params["message"])), "database") {
				t.Fatalf("question does not mention the database: %v", params["message"])
			}
			askID = id
		case <-deadline:
			t.Fatal("timed out waiting for the agent's question")
		}
	}

	// Let the turn sit on the question for a moment — it must NOT end.
	select {
	case err := <-promptDone:
		t.Fatalf("turn ended (%v) while the question was unanswered — the ask did not block", err)
	case <-time.After(5 * time.Second):
	}
	answeredAt := time.Now()

	answer := fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"result":{"action":"accept","content":{"answer":%q}}}`, askID, askMarker)
	if err := viewer.WriteMessage(websocket.TextMessage, []byte(answer)); err != nil {
		t.Fatalf("answer write: %v", err)
	}
	t.Log("viewer answered the question")

	if err := <-promptDone; err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if ended := <-promptEnded; ended.Before(answeredAt) {
		t.Fatalf("turn ended before the answer was sent (ended %v, answered %v)", ended, answeredAt)
	}

	// Judge the final answer from the teed stream.
	var agentText strings.Builder
	flush := time.After(2 * time.Second)
drain:
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				break drain
			}
			params, _ := f["params"].(map[string]any)
			update, _ := params["update"].(map[string]any)
			if kind, _ := update["sessionUpdate"].(string); kind == "agent_message_chunk" {
				if content, _ := update["content"].(map[string]any); content != nil {
					text, _ := content["text"].(string)
					agentText.WriteString(text)
				}
			}
		case <-flush:
			break drain
		}
	}
	final := agentText.String()
	t.Logf("agent transcript (%d chars): %s", len(final), final)
	if !strings.Contains(strings.ToLower(final), askMarker) {
		t.Fatalf("final answer does not reflect the human's answer (%q missing)", askMarker)
	}
}
