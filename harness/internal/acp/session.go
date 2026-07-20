package acp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/konveyor/migration-harness/internal/logging"
)

// SessionClient wraps WSClient with ACP session operations.
type SessionClient struct {
	ws          *WSClient
	initialized bool
}

// NewSessionClient creates a session client from an existing WebSocket connection.
func NewSessionClient(ws *WSClient) *SessionClient {
	return &SessionClient{ws: ws}
}

// InitParams are required for the ACP initialize handshake.
type InitParams struct {
	ProtocolVersion string     `json:"protocolVersion"`
	ClientInfo      ClientInfo `json:"clientInfo"`
	Capabilities    struct{}   `json:"capabilities"`
}

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitResult is the response from initialize.
type InitResult struct {
	ProtocolVersion  int             `json:"protocolVersion"`
	AgentCapabilities json.RawMessage `json:"agentCapabilities"`
}

// Initialize performs the required ACP handshake. Must be called before
// any session operations. protocolVersion is required — goose returns a
// parse error without it.
func (c *SessionClient) Initialize(ctx context.Context) (*InitResult, error) {
	if c.initialized {
		return nil, nil
	}

	result, _, err := c.ws.Call(ctx, "initialize", &InitParams{
		ProtocolVersion: "0.1",
		ClientInfo: ClientInfo{
			Name:    "migration-harness",
			Version: "0.1.0",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}

	var initResult InitResult
	if err := json.Unmarshal(result, &initResult); err != nil {
		return nil, fmt.Errorf("parse initialize result: %w", err)
	}

	c.initialized = true
	logging.Ok("ACP initialized (protocol version %d)", initResult.ProtocolVersion)
	return &initResult, nil
}

// SessionNewParams for session/new.
type SessionNewParams struct {
	CWD        string      `json:"cwd"`
	MCPServers []MCPServer `json:"mcpServers"`
}

// MCPServer describes an MCP tool server for a session.
type MCPServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

// SessionNewResult is the response from session/new.
type SessionNewResult struct {
	SessionID string          `json:"sessionId"`
	Modes     json.RawMessage `json:"modes,omitempty"`
	Models    json.RawMessage `json:"models,omitempty"`
}

// CreateSession creates a new ACP session. The session ID comes from a
// session/update notification before the response — this is confirmed
// behavior from goose 1.33.1.
func (c *SessionClient) CreateSession(ctx context.Context, cwd string, mcpServers []MCPServer) (string, error) {
	if !c.initialized {
		if _, err := c.Initialize(ctx); err != nil {
			return "", err
		}
	}

	if mcpServers == nil {
		mcpServers = []MCPServer{}
	}

	result, notifications, err := c.ws.Call(ctx, "session/new", &SessionNewParams{
		CWD:        cwd,
		MCPServers: mcpServers,
	})
	if err != nil {
		return "", fmt.Errorf("session/new: %w", err)
	}

	// Session ID may come from a notification before the response
	sessionID := extractSessionIDFromNotifications(notifications)

	// Also check the response
	if sessionID == "" {
		var newResult SessionNewResult
		if err := json.Unmarshal(result, &newResult); err == nil && newResult.SessionID != "" {
			sessionID = newResult.SessionID
		}
	}

	if sessionID == "" {
		return "", fmt.Errorf("session/new: no session ID received")
	}

	logging.Ok("ACP session created: %s", sessionID[:8]+"...")
	return sessionID, nil
}

// ContentBlock is a content item in a prompt.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// PromptParams for session/prompt.
type PromptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

// PromptResult is the final response from session/prompt.
type PromptResult struct {
	StopReason string          `json:"stopReason"`
	Usage      *PromptUsage    `json:"usage,omitempty"`
	Chunks     []string        // collected agent_message_chunk text
}

type PromptUsage struct {
	TotalTokens  int `json:"totalTokens"`
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

// SendPrompt sends a prompt to a session and collects the streaming
// response. Returns the final result with all collected message chunks.
func (c *SessionClient) SendPrompt(ctx context.Context, sessionID string, content []ContentBlock) (*PromptResult, error) {
	req := newRequest("session/prompt", &PromptParams{
		SessionID: sessionID,
		Prompt:    content,
	})

	if err := c.ws.Send(req); err != nil {
		return nil, fmt.Errorf("send prompt: %w", err)
	}

	result := &PromptResult{}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.ws.Done():
			return nil, fmt.Errorf("websocket connection closed during prompt")
		case msg := <-c.ws.Recv():
			if msg.IsNotification() {
				handlePromptNotification(msg, result)
				continue
			}
			if msg.ID != nil && *msg.ID == req.ID {
				if msg.Error != nil {
					return nil, fmt.Errorf("prompt error %d: %s", msg.Error.Code, msg.Error.Message)
				}
				if err := json.Unmarshal(msg.Result, result); err != nil {
					return nil, fmt.Errorf("parse prompt result: %w", err)
				}
				return result, nil
			}
		}
	}
}

func extractSessionIDFromNotifications(notifications []*RPCResponse) string {
	for _, n := range notifications {
		if n.Method != "session/update" {
			continue
		}
		var params struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(n.Params, &params); err == nil && params.SessionID != "" {
			return params.SessionID
		}
	}
	return ""
}

func handlePromptNotification(msg *RPCResponse, result *PromptResult) {
	if msg.Method != "session/update" {
		return
	}

	var params struct {
		Update struct {
			SessionUpdate string          `json:"sessionUpdate"`
			Content       json.RawMessage `json:"content,omitempty"`
			Title         string          `json:"title,omitempty"`
			Status        string          `json:"status,omitempty"`
			Text          string          `json:"text,omitempty"`
			Type          string          `json:"type,omitempty"`
		} `json:"update"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return
	}

	switch params.Update.SessionUpdate {
	case "agent_message_chunk":
		var content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(params.Update.Content, &content); err == nil {
			result.Chunks = append(result.Chunks, content.Text)
		}
	case "tool_call":
		logging.Info("  tool: %s (%s)", params.Update.Title, params.Update.Status)
	case "tool_call_update":
		if params.Update.Status == "completed" || params.Update.Status == "failed" {
			logging.Info("  tool: %s", params.Update.Status)
		}
	}
}
