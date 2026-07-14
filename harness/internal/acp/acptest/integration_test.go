// go test -v -tags=integration -run TestACP ./internal/acp/acptest/
// Requires goose installed and a configured LLM provider.

//go:build integration

package acptest

import (
	"context"
	"testing"
	"time"

	"github.com/konveyor/migration-harness/internal/acp"
	"github.com/konveyor/migration-harness/internal/goose"
)

func TestACPIntegrationDefaultPort(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	srv, err := goose.StartServe(ctx, 0, "", "", "", "")
	if err != nil {
		t.Fatalf("StartServe: %v", err)
	}
	defer srv.Stop()

	if srv.Port() != goose.DefaultACPPort {
		t.Fatalf("expected default port %d, got %d", goose.DefaultACPPort, srv.Port())
	}
	t.Logf("Sandbox mode: port %d", srv.Port())

	client, err := acp.WaitReadyDial(ctx, "127.0.0.1", srv.Port(), srv.SecretKey(), 15*time.Second)
	if err != nil {
		t.Fatalf("WaitReadyDial: %v", err)
	}
	defer client.Close()

	session := acp.NewSessionClient(client)

	initResult, err := session.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	t.Logf("Protocol version: %d", initResult.ProtocolVersion)

	sessionID, err := session.CreateSession(ctx, "/tmp", nil)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Logf("Session ID: %s", sessionID)

	result, err := session.SendPrompt(ctx, sessionID, []acp.ContentBlock{
		{Type: "text", Text: "Respond with exactly one word: hello"},
	})
	if err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}

	t.Logf("Stop reason: %s, Chunks: %v", result.StopReason, result.Chunks)
	if result.StopReason != "end_turn" {
		t.Errorf("expected 'end_turn', got %q", result.StopReason)
	}
}

func TestACPIntegrationFreePort(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	port, err := goose.FindFreePort()
	if err != nil {
		t.Fatalf("FindFreePort: %v", err)
	}
	srv, err := goose.StartServe(ctx, port, "", "", "", "")
	if err != nil {
		t.Fatalf("StartServe: %v", err)
	}
	defer srv.Stop()

	client, err := acp.WaitReadyDial(ctx, "127.0.0.1", srv.Port(), srv.SecretKey(), 15*time.Second)
	if err != nil {
		t.Fatalf("WaitReadyDial: %v", err)
	}
	defer client.Close()

	session := acp.NewSessionClient(client)

	initResult, err := session.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	t.Logf("Protocol version: %d", initResult.ProtocolVersion)

	sessionID, err := session.CreateSession(ctx, "/tmp", nil)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Logf("Session ID: %s", sessionID)

	result, err := session.SendPrompt(ctx, sessionID, []acp.ContentBlock{
		{Type: "text", Text: "Respond with exactly one word: hello"},
	})
	if err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}

	t.Logf("Stop reason: %s, Chunks: %v", result.StopReason, result.Chunks)
	if result.Usage != nil {
		t.Logf("Tokens: %d total (%d in, %d out)",
			result.Usage.TotalTokens, result.Usage.InputTokens, result.Usage.OutputTokens)
	}

	if result.StopReason != "end_turn" {
		t.Errorf("expected 'end_turn', got %q", result.StopReason)
	}
	if len(result.Chunks) == 0 {
		t.Error("expected at least one message chunk")
	}
}
