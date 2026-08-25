package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/konveyor/migration-harness/internal/logging"
)

// ErrConnectionLost is returned by SendPrompt when the websocket closes
// abruptly mid-prompt with no final response. Confirmed empirically: this
// goose version does not report a graceful stopReason when its native
// GOOSE_MAX_TURNS limit is hit — it drops the connection instead. Callers
// combine this with the returned (partial) *PromptResult's TurnsUsed to
// distinguish a likely turn-limit hit (real progress, then disconnect)
// from a genuine early failure (disconnect before any turn ran).
var ErrConnectionLost = errors.New("websocket connection closed during prompt")

// SessionClient wraps WSClient with ACP session operations.
type SessionClient struct {
	ws          *WSClient
	initialized bool

	fwdMu     sync.Mutex
	forwarder PermissionForwarder

	// sessionID is the run session, stored so an unanswered HITL question
	// can cancel the turn from the agent-request goroutine. Written once in
	// CreateSession (before any prompt), read on elicitation timeout.
	sessionID atomic.Value // string
	// hitlUnanswered latches when an ask_user question went unanswered, so
	// runStage fails the run instead of letting the model guess past it.
	hitlUnanswered atomic.Bool
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

	c.sessionID.Store(sessionID)

	preview := sessionID
	if len(preview) > 8 {
		preview = preview[:8] + "..."
	}
	logging.Ok("ACP session created: %s", preview)
	return sessionID, nil
}

// HITLGateUnanswered reports whether the run raised an ask_user question
// that no human answered. The harness treats that as a terminal failure:
// the agent explicitly asked for a decision it could not make alone, so a
// run that proceeds anyway is exactly the fail-open the gate exists to
// prevent. Read by runStage once the prompt returns.
func (c *SessionClient) HITLGateUnanswered() bool {
	return c.hitlUnanswered.Load()
}

// cancelTurn stops the active run turn. Fired after an unanswered
// elicitation is cancelled: goose parks the turn on the elicitation reply
// with no timeout of its own and session/cancel cannot unpark it — the
// cancel action is the only key — so this must run only after the cancel
// response has been sent, when the turn is running again and can be
// stopped before the model steamrolls a guess.
func (c *SessionClient) cancelTurn() {
	sid, _ := c.sessionID.Load().(string)
	if sid == "" {
		return
	}
	if err := c.ws.Notify("session/cancel", map[string]any{"sessionId": sid}); err != nil {
		logging.Warn("cancel turn after unanswered HITL question: %v", err)
	}
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

	CostLimitReached bool `json:"-"`
	// Cost is the last cost.amount observed during THIS call via
	// usage_update (0 if the provider never reported one — ACP 0011
	// documents cost as optional).
	Cost float64 `json:"-"`
	// TurnsUsed counts tool_call notifications observed during THIS
	// call. Reporting only — nothing reads this to decide when to stop;
	// the runtime enforces turn limits natively now (ADR 0011).
	TurnsUsed int `json:"-"`
	// ContextUsed / ContextSize are the last usage_update occupancy
	// snapshot observed during THIS call (0 if never reported).
	ContextUsed int `json:"-"`
	ContextSize int `json:"-"`

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
// response. costLimit is the cumulative usage_update cost.amount
// threshold above which the harness sends its own session/cancel; 0
// disables cost monitoring. Turn limits are enforced natively by the
// runtime (GOOSE_MAX_TURNS) — SendPrompt does not count turns to decide
// when to stop; TurnsUsed on the result is for reporting only (ADR
// 0011).
func (c *SessionClient) SendPrompt(ctx context.Context, sessionID string, content []ContentBlock, costLimit float64) (*PromptResult, error) {
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
	sawCost := false
	cancelSent := false

	process := func(n *RPCResponse) {
		if isToolCall(n) {
			result.TurnsUsed++
		}
		handlePromptNotification(n, result)
		trackUsage(n, result, &sawCost)
	}

	maybeCancelForCost := func() {
		if costLimit > 0 && !cancelSent && result.Cost >= costLimit {
			cancelSent = true
			result.CostLimitReached = true
			if err := c.ws.Notify("session/cancel", map[string]any{"sessionId": sessionID}); err != nil {
				logging.Warn("send session/cancel for cost limit: %v", err)
			}
		}
	}

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
					process(n)
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
				for _, n := range drainNotifications(notifCh) {
					process(n)
				}
				return result, ErrConnectionLost
			}
		case msg := <-notifCh:
			process(msg)
			maybeCancelForCost()
		case msg := <-respCh:
			// Notifications buffered before the response still belong to
			// this turn — drain them so a trailing chunk is not lost when
			// select picks respCh first (mirrors WSClient.Call).
			for _, n := range drainNotifications(notifCh) {
				process(n)
			}
			if msg.Error != nil {
				// Hand back what streamed before the error: the closing
				// message is worth most when the turn ends this way.
				return result, fmt.Errorf("prompt error %d: %w", msg.Error.Code, msg.Error)
			}
			if err := json.Unmarshal(msg.Result, result); err != nil {
				return nil, fmt.Errorf("parse prompt result: %w", err)
			}
			if costLimit > 0 && !sawCost {
				logging.Warn("maxCost is configured but no usage_update reported cost — cost enforcement had no effect on this prompt")
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
// (elicitation/create — from the harness's ask_user tool, the only
// elicitation source, mounted only when HITL asking is enabled). Offered to
// attached viewers first; a human's answer rides back verbatim. With nobody
// there, or nobody answering in time, the ask is a HITL gate the agent
// explicitly opened and cannot clear alone: the harness cancels the ask AND
// fails the run — it stops the turn (fail closed) rather than let the model
// read "nobody answered" and steamroll a guess. Never answered on the
// human's behalf.
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
			logging.Warn("question %q unanswered by viewer — cancelling and failing the run (HITL gate)", title)
		case ForwardNoViewers:
			logging.Warn("question %q — no viewer to answer, cancelling and failing the run (HITL gate)", title)
		}
	} else {
		logging.Warn("agent asked %q — no HITL relay, cancelling and failing the run", title)
	}

	// Latch the failure before unparking goose, then send the cancel action
	// (the only thing that unparks the turn) and stop the now-running turn
	// so the model cannot proceed on an unanswered gate.
	c.hitlUnanswered.Store(true)
	if err := c.ws.SendResponse(id, map[string]any{"action": "cancel"}, nil); err != nil {
		logging.Warn("reply to elicitation: %v", err)
	}
	c.cancelTurn()
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
// Duplicated from the tee package's own constant of the same name —
// these are two independent JSON-RPC clients and neither package
// imports the other's internals.
const gooseUpdateMethod = "_goose/unstable/session/update"

// trackUsage extracts cost/context data from a usage_update frame
// (ADR 0011) into result. A frame with no cost field leaves
// result.Cost unchanged rather than resetting it to zero — cost is
// documented as optional (ADR 0011: "optional cost data"); sawCost
// distinguishes "never reported" from "reported and zero" so the
// caller can warn when cost enforcement never got any data at all.
//
// Context occupancy's field name is unconfirmed against real goose:
// ADR 0011's example JSON uses "size", but this repo's own tee test
// fixture (internal/tee/tee_test.go) uses "contextLimit" for the same
// concept. Both are accepted defensively; "size" wins if a frame
// somehow carries both.
func trackUsage(msg *RPCResponse, result *PromptResult, sawCost *bool) {
	if msg.Method != gooseUpdateMethod {
		return
	}
	var params struct {
		Update struct {
			SessionUpdate string `json:"sessionUpdate"`
			Used          int    `json:"used"`
			Size          int    `json:"size"`
			ContextLimit  int    `json:"contextLimit"`
			Cost          *struct {
				Amount float64 `json:"amount"`
			} `json:"cost"`
		} `json:"update"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return
	}
	if params.Update.SessionUpdate != "usage_update" {
		return
	}
	if params.Update.Cost != nil {
		result.Cost = params.Update.Cost.Amount
		*sawCost = true
	}
	size := params.Update.Size
	if size == 0 {
		size = params.Update.ContextLimit
	}
	if size > 0 {
		result.ContextUsed = params.Update.Used
		result.ContextSize = size
	}
}
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
