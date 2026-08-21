package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
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

// PermissionForwarder relays a session/request_permission ask to attached
// human viewers (the ACP tee). Implementations must be safe for concurrent
// use and must not block past their own timeout.
type PermissionForwarder interface {
	ForwardPermission(params json.RawMessage) (json.RawMessage, PermissionForwardOutcome)
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
	Meta map[string]any `json:"_meta,omitempty"`
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
		ClientCapabilities: ClientCapabilities{
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

	// closing collects the agent messages streamed after the most recent
	// tool call (or the latest human steer): what the model says when it
	// stops. Text before a tool call is narration about work it then does;
	// the turn's conclusion is what follows the last call. One entry per
	// goose message, so distinct messages are not run together.
	closing []string
	// closingMessageID is the goose message id the last closing entry
	// belongs to.
	closingMessageID string
	// Notices are goose's own status notices for the turn
	// (`_goose/unstable/session/update` status_message/notice), e.g.
	// "Unable to continue: Context limit still exceeded after compaction".
	// They are not agent text and would otherwise vanish with the pod.
	Notices []string
}

// FinalMessage is the agent's closing message for the turn — the text after
// the last tool call, or the whole reply when the agent called no tool. It
// is also where goose reports most provider failures: the reply loop turns
// them into assistant prose ("Ran into this error: …") and ends the turn
// normally, so the client never sees an RPC error.
func (r *PromptResult) FinalMessage() string {
	return strings.Join(r.closing, "\n")
}

// ClosingProviderError reports whether any message in the closing text is
// one of goose's provider-failure messages (see LooksLikeProviderError).
func (r *PromptResult) ClosingProviderError() bool {
	return slices.ContainsFunc(r.closing, LooksLikeProviderError)
}

// appendClosing adds a chunk to the closing text, continuing the current
// goose message when the id matches and starting a new one otherwise.
func (r *PromptResult) appendClosing(text, messageID string) {
	if len(r.closing) > 0 && (messageID == "" || messageID == r.closingMessageID) {
		r.closing[len(r.closing)-1] += text
	} else {
		r.closing = append(r.closing, text)
	}
	r.closingMessageID = messageID
}

// goose's provider-failure messages are whole assistant messages with
// fixed openings or trailers (crates/goose/src/agents/agent.rs: the reply
// loop's error arms, compaction failures, and the empty-response fallback).
// Matching is anchored to the message so an agent quoting one of these
// phrases in its own summary is not mistaken for a failure. Credits
// exhaustion is not here: over ACP goose turns it into the session/prompt
// RPC error, which SendPrompt now prints with its data.
var (
	providerErrorPrefixes = []string{
		"Ran into this error",
		"The provider refused this request.",
		"The model returned an empty response.",
	}
	providerErrorSuffixes = []string{
		"Please resend your message to try again.",
	}
)

// LooksLikeProviderError reports whether one agent message is, or ends
// with, one of goose's provider-failure messages. A turn ending on one
// never reached the model, or lost it mid-way, yet from the outside it
// looks like an ordinary end of turn. goose emits the failure as its own
// message, but text it had already streamed can precede it in the same
// entry when no message id separates them, so prefixes are also checked
// at the start of each line.
func LooksLikeProviderError(text string) bool {
	t := strings.TrimSpace(text)
	for _, m := range providerErrorSuffixes {
		if strings.HasSuffix(t, m) {
			return true
		}
	}
	for line := range strings.SplitSeq(t, "\n") {
		line = strings.TrimSpace(line)
		for _, m := range providerErrorPrefixes {
			if strings.HasPrefix(line, m) {
				return true
			}
		}
	}
	return false
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
					// Hand back what streamed before the error: the closing
					// message is worth most when the turn ends this way.
					return result, fmt.Errorf("prompt error %d: %w", msg.Error.Code, msg.Error)
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
				if maxTurns > 0 && turnCount >= maxTurns {
					// Decide before handling the call: the text that
					// introduced it is the last thing the agent said.
					logging.Warn("max turns reached (%d), terminating", maxTurns)
					return result, fmt.Errorf("max turns reached (%d)", maxTurns)
				}
			}
			handlePromptNotification(msg, result)
		case msg := <-respCh:
			// Notifications buffered before the response still belong to
			// this turn — drain them so a trailing chunk is not lost when
			// select picks respCh first (mirrors WSClient.Call).
			for _, n := range drainNotifications(notifCh) {
				handlePromptNotification(n, result)
			}
			if msg.Error != nil {
				// Hand back what streamed before the error: the closing
				// message is worth most when the turn ends this way.
				return result, fmt.Errorf("prompt error %d: %w", msg.Error.Code, msg.Error)
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

// gooseUpdateMethod carries goose's custom session updates (usage,
// status notices); the harness declares the capability in Initialize.
const gooseUpdateMethod = "_goose/unstable/session/update"

func handlePromptNotification(msg *RPCResponse, result *PromptResult) {
	if msg.Method == gooseUpdateMethod {
		handleGooseNotice(msg, result)
		return
	}
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
			Meta          struct {
				Goose struct {
					MessageID string `json:"messageId"`
				} `json:"goose"`
			} `json:"_meta"`
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
		if err := json.Unmarshal(params.Update.Content, &content); err == nil && content.Type == "text" {
			result.Chunks = append(result.Chunks, content.Text)
			result.appendClosing(content.Text, params.Update.Meta.Goose.MessageID)
		}
	case "user_message_chunk":
		// A human steered the run mid-turn; the closing message is the
		// reply to that, not what preceded it.
		result.closing = nil
	case "tool_call":
		logging.Info("  tool: %s (%s)", params.Update.Title, params.Update.Status)
		// Whatever was said so far introduced this call; the closing
		// message starts over after it.
		result.closing = nil
	case "tool_call_update":
		if params.Update.Status == "completed" || params.Update.Status == "failed" {
			logging.Info("  tool: %s", params.Update.Status)
		}
	}
}

// handleGooseNotice records goose's status notices. goose sends its own
// terminal failures this way rather than as agent text — "Unable to
// continue: Context limit still exceeded after compaction" — and the tee
// only relays them to whoever is watching.
func handleGooseNotice(msg *RPCResponse, result *PromptResult) {
	var params struct {
		Update struct {
			SessionUpdate string `json:"sessionUpdate"`
			Status        struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"status"`
		} `json:"update"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return
	}
	if params.Update.SessionUpdate != "status_message" || params.Update.Status.Type != "notice" {
		return
	}
	if m := strings.TrimSpace(params.Update.Status.Message); m != "" {
		logging.Info("  goose: %s", m)
		result.Notices = append(result.Notices, m)
	}
}
