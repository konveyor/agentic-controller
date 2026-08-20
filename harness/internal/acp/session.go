package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/konveyor/migration-harness/internal/logging"
)

// SessionClient wraps WSClient with ACP session operations.
type SessionClient struct {
	ws          *WSClient
	initialized bool

	fwdMu     sync.Mutex
	forwarder PermissionForwarder
}

// NewSessionClient creates a session client from an existing WebSocket
// connection and takes over answering agent-initiated requests on it.
func NewSessionClient(ws *WSClient) *SessionClient {
	c := &SessionClient{ws: ws}
	ws.SetAgentRequestHandler(c.answerAgentRequest)
	return c
}

// PermissionForwardOutcome says what happened when a permission ask was
// offered to attached viewers.
type PermissionForwardOutcome int

const (
	// ForwardNoViewers: nobody is attached; the caller applies the
	// headless fail-closed policy.
	ForwardNoViewers PermissionForwardOutcome = iota
	// ForwardAnswered: a viewer answered; the result is their
	// RequestPermissionResponse result object, to relay verbatim.
	ForwardAnswered
	// ForwardTimeout: viewers were attached but none answered in time.
	// The caller applies the same fail-closed deny as ForwardNoViewers;
	// the forwarder additionally marks viewers unresponsive so follow-up
	// asks resolve fast until a human shows signs of life again.
	ForwardTimeout
)

// PermissionForwarder relays the asks goose raises toward the client —
// session/request_permission (tool approval) and elicitation/create (a
// question from the agent, e.g. the ask_user tool) — to attached human
// viewers (the ACP tee). Implementations must be safe for concurrent use
// and must not block past their own timeout.
type PermissionForwarder interface {
	ForwardPermission(params json.RawMessage) (json.RawMessage, PermissionForwardOutcome)
	ForwardElicitation(params json.RawMessage) (json.RawMessage, PermissionForwardOutcome)
}

// SetPermissionForwarder installs the viewer relay consulted before the
// fail-closed deny in answerAgentRequest.
func (c *SessionClient) SetPermissionForwarder(f PermissionForwarder) {
	c.fwdMu.Lock()
	c.forwarder = f
	c.fwdMu.Unlock()
}

func (c *SessionClient) permissionForwarder() PermissionForwarder {
	c.fwdMu.Lock()
	defer c.fwdMu.Unlock()
	return c.forwarder
}

// InitParams are required for the ACP initialize handshake.
type InitParams struct {
	ProtocolVersion string     `json:"protocolVersion"`
	ClientInfo      ClientInfo `json:"clientInfo"`
	// ClientCapabilities is the ACP field name (the earlier "capabilities"
	// spelling was never read by goose). The goose extension point lives
	// under _meta.goose.
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
}

type ClientCapabilities struct {
	// Elicitation advertises form-mode elicitation support: goose then
	// relays an MCP server's elicitation/create (the harness's own
	// ask_user tool) to us instead of cancelling it on the agent's behalf.
	Elicitation map[string]any `json:"elicitation,omitempty"`
	Meta        map[string]any `json:"_meta,omitempty"`
}

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitResult is the response from initialize.
type InitResult struct {
	ProtocolVersion   int             `json:"protocolVersion"`
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
		// customNotifications turns on goose's `_goose/unstable/session/update`
		// stream: usage_update (live token/context spend) and status_message
		// (notices + progress). The tee forwards both to attached viewers.
		// elicitation.form makes goose route an agent's question
		// (elicitation/create) to the harness, which offers it to viewers
		// like a permission ask — and fails closed (cancel) with nobody there.
		ClientCapabilities: ClientCapabilities{
			Elicitation: map[string]any{"form": map[string]any{}},
			Meta: map[string]any{
				"goose": map[string]any{"customNotifications": true},
			},
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

// MCPServer describes a stdio MCP tool server for a session (ACP
// McpServerStdio: name, command, args, env as a LIST of name/value pairs —
// a map is rejected by goose's untagged-enum parse with -32602).
type MCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Env     []EnvVar `json:"env"`
}

// EnvVar is one environment variable handed to an MCP server.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// MarshalJSON keeps args and env present-and-array on the wire even when
// nil: goose's untagged-enum parse of mcpServers is only proven against
// that exact shape, and a nil slice would marshal as null — risking the
// same -32602 the name/value list exists to avoid. Whether an absent
// field matches the stdio variant is equally unproven, so nil normalizes
// to [] rather than relying on omitempty.
func (m MCPServer) MarshalJSON() ([]byte, error) {
	type mcpServerNoMethods MCPServer
	w := mcpServerNoMethods(m)
	if w.Args == nil {
		w.Args = []string{}
	}
	if w.Env == nil {
		w.Env = []EnvVar{}
	}
	return json.Marshal(w)
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

	preview := sessionID
	if len(preview) > 8 {
		preview = preview[:8] + "..."
	}
	logging.Ok("ACP session created: %s", preview)
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
	StopReason string       `json:"stopReason"`
	Usage      *PromptUsage `json:"usage,omitempty"`
	Chunks     []string     `json:"-"`
}

type PromptUsage struct {
	TotalTokens  int `json:"totalTokens"`
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

// SendPrompt sends a prompt to a session and collects the streaming
// response. maxTurns limits the number of tool calls before the prompt
// is terminated. If maxTurns is 0, no limit is enforced.
func (c *SessionClient) SendPrompt(ctx context.Context, sessionID string, content []ContentBlock, maxTurns int) (*PromptResult, error) {
	req := newRequest("session/prompt", &PromptParams{
		SessionID: sessionID,
		Prompt:    content,
	})

	respCh := c.ws.registerPending(req.ID)
	defer c.ws.unregisterPending(req.ID)
	sinkID, notifCh := c.ws.addNotifSink()
	defer c.ws.removeNotifSink(sinkID)

	if err := c.ws.Send(req); err != nil {
		return nil, fmt.Errorf("send prompt: %w", err)
	}

	result := &PromptResult{}
	turnCount := 0

	// Agent-initiated requests (permission asks) no longer appear here —
	// the demux dispatches them to answerAgentRequest on their own
	// goroutine, so a parked ask cannot stall notification handling.
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.ws.Done():
			// readLoop exited. The response may already be routed to
			// respCh — drain it before giving up.
			select {
			case msg := <-respCh:
				for _, n := range drainNotifications(notifCh) {
					handlePromptNotification(n, result)
				}
				if msg.Error != nil {
					return nil, fmt.Errorf("prompt error %d: %s", msg.Error.Code, msg.Error.Message)
				}
				if err := json.Unmarshal(msg.Result, result); err != nil {
					return nil, fmt.Errorf("parse prompt result: %w", err)
				}
				return result, nil
			default:
				return nil, fmt.Errorf("websocket connection closed during prompt")
			}
		case msg := <-notifCh:
			if isToolCall(msg) {
				turnCount++
			}
			handlePromptNotification(msg, result)
			if maxTurns > 0 && turnCount >= maxTurns {
				logging.Warn("max turns reached (%d), terminating", maxTurns)
				return result, fmt.Errorf("max turns reached (%d)", maxTurns)
			}
		case msg := <-respCh:
			// Notifications buffered before the response still belong to
			// this turn — drain them so a trailing chunk is not lost when
			// select picks respCh first (mirrors WSClient.Call).
			for _, n := range drainNotifications(notifCh) {
				handlePromptNotification(n, result)
			}
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

// PermissionOption is one choice offered by a session/request_permission
// request (kinds: allow_always, allow_once, reject_once, reject_always).
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name,omitempty"`
	Kind     string `json:"kind,omitempty"`
}

// answerAgentRequest replies to a request goose initiates toward the client
// (session/request_permission, elicitation/create, fs/*). These frames were
// previously dropped on the floor — and goose parks the turn on the reply
// with NO timeout; session/cancel cannot unpark it. Any session that enters
// approve mode (GOOSE_MODE=approve / session/set_mode), or a SecurityInspector
// escalation via SECURITY_PROMPT_ENABLED even in auto mode, would hang the
// stage until the pod deadline.
//
// Permission asks are offered to attached viewers first (the ACP tee): a
// human watching the run answers, and their outcome is relayed verbatim.
// With nobody attached the harness is headless and the policy is
// fail-closed: deny permission requests explicitly (goose declines the
// tool and the turn continues) and reject everything else with
// method-not-found (goose maps that to a cancelled/declined outcome too).
func (c *SessionClient) answerAgentRequest(msg *RPCResponse) {
	id := msg.ID

	if msg.Method == "elicitation/create" {
		c.answerElicitation(msg)
		return
	}

	if msg.Method != "session/request_permission" {
		logging.Warn("agent request %q unsupported — rejecting (method not found)", msg.Method)
		if err := c.ws.SendResponse(id, nil, &RPCError{Code: -32601, Message: "method not supported by harness"}); err != nil {
			logging.Warn("reply to %s: %v", msg.Method, err)
		}
		return
	}

	var params struct {
		ToolCall struct {
			Title string `json:"title"`
		} `json:"toolCall"`
		Options []PermissionOption `json:"options"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		logging.Warn("parse permission request: %v — cancelling it", err)
		if err := c.ws.SendResponse(id, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}, nil); err != nil {
			logging.Warn("reply to permission request: %v", err)
		}
		return
	}

	if f := c.permissionForwarder(); f != nil {
		result, outcome := f.ForwardPermission(msg.Params)
		switch outcome {
		case ForwardAnswered:
			logging.Info("permission for %q answered by attached viewer", params.ToolCall.Title)
			if err := c.ws.SendResponse(id, result, nil); err != nil {
				logging.Warn("relay permission answer: %v", err)
			}
			return
		case ForwardTimeout:
			// A viewer was attached but nobody answered. Fail closed —
			// an ask that self-approves on a timer is no ask at all. The
			// forwarder marks viewers unresponsive after a timeout, so
			// follow-up asks (goose retrying the declined tool) deny
			// fast instead of waiting out the clock each time.
			logging.Warn("permission for %q unanswered by viewer — denying (fail closed)", params.ToolCall.Title)
		case ForwardNoViewers:
			// fall through to the headless deny
		}
	}

	// Prefer an explicit one-shot rejection; an unknown or missing option
	// falls back to the cancelled outcome, which goose also treats as a
	// decline (fail-closed on its side too).
	outcome := map[string]any{"outcome": "cancelled"}
	for _, opt := range params.Options {
		if opt.Kind == "reject_once" {
			outcome = map[string]any{"outcome": "selected", "optionId": opt.OptionID}
			break
		}
	}
	logging.Warn("goose asked permission for %q — headless harness denies it", params.ToolCall.Title)
	if err := c.ws.SendResponse(id, map[string]any{"outcome": outcome}, nil); err != nil {
		logging.Warn("reply to permission request: %v", err)
	}
}

// answerElicitation handles the agent asking the human a question
// (elicitation/create — from the harness's ask_user tool or any MCP server
// the session mounts). Offered to attached viewers first; with nobody
// there, or nobody answering in time, the ask is cancelled — goose reports
// that back to the tool, which tells the model no human answered. Never
// answered on the human's behalf.
func (c *SessionClient) answerElicitation(msg *RPCResponse) {
	id := msg.ID
	var params struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(msg.Params, &params)
	title := params.Message
	// Truncate on rune boundaries: byte slicing a non-ASCII message would
	// split a UTF-8 sequence mid-rune.
	if r := []rune(title); len(r) > 80 {
		title = string(r[:77]) + "..."
	}

	if f := c.permissionForwarder(); f != nil {
		result, outcome := f.ForwardElicitation(msg.Params)
		switch outcome {
		case ForwardAnswered:
			logging.Info("question %q answered by attached viewer", title)
			if err := c.ws.SendResponse(id, result, nil); err != nil {
				logging.Warn("relay elicitation answer: %v", err)
			}
			return
		case ForwardTimeout:
			logging.Warn("question %q unanswered by viewer — cancelling (fail closed)", title)
		case ForwardNoViewers:
			// fall through to the headless cancel
		}
	}

	logging.Warn("agent asked %q — headless harness cancels it (no human to answer)", title)
	if err := c.ws.SendResponse(id, map[string]any{"action": "cancel"}, nil); err != nil {
		logging.Warn("reply to elicitation: %v", err)
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

func isToolCall(msg *RPCResponse) bool {
	if msg.Method != "session/update" {
		return false
	}
	var params struct {
		Update struct {
			SessionUpdate string `json:"sessionUpdate"`
		} `json:"update"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return false
	}
	return params.Update.SessionUpdate == "tool_call"
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
