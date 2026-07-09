package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

var ErrNotImplemented = errors.New("ACP client not implemented")

type Response struct {
	Content json.RawMessage
	Done    bool
}

type Event struct {
	Type string          // "tool_call", "thinking", "progress", "error"
	Data json.RawMessage
}

// Client is the interface for ACP communication with the agent runtime.
// The harness launches `goose serve --port <port>` and uses this client
// to send instructions and receive responses.
type Client interface {
	SendInstruction(ctx context.Context, instruction string, params map[string]string) (Response, error)
	StreamEvents(ctx context.Context) (<-chan Event, error)
	Close() error
}

// StubClient returns ErrNotImplemented for all methods.
// Replace with a real implementation when goose serve ACP is ready.
type StubClient struct{}

func NewStubClient() *StubClient {
	return &StubClient{}
}

func (s *StubClient) SendInstruction(ctx context.Context, instruction string, params map[string]string) (Response, error) {
	return Response{}, ErrNotImplemented
}

func (s *StubClient) StreamEvents(ctx context.Context) (<-chan Event, error) {
	return nil, ErrNotImplemented
}

func (s *StubClient) Close() error {
	return nil
}

// LaunchGooseServe starts `goose serve --port <port>` as a background process.
func LaunchGooseServe(ctx context.Context, port int) (*os.Process, error) {
	goosePath, err := exec.LookPath("goose")
	if err != nil {
		return nil, fmt.Errorf("goose not found: %w", err)
	}

	cmd := exec.CommandContext(ctx, goosePath, "serve", "--port", fmt.Sprintf("%d", port))
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start goose serve: %w", err)
	}

	return cmd.Process, nil
}
