// Package askuser is a minimal MCP (Model Context Protocol) server, spoken
// over stdio, that gives the agent exactly one tool: ask_user.
//
// Calling the tool becomes an MCP elicitation/create request back to goose
// (the MCP client); goose relays it over ACP to the harness as
// elicitation/create — gated on the clientCapabilities.elicitation.form the
// harness advertises at initialize — the harness offers it to attached
// viewers through the tee, and the human's answer travels the same road
// back. The tool call blocks for the whole round trip, which is the point:
// the agent genuinely waits for a decision instead of asking in prose that
// nobody reads until the run is over.
//
// goose starts this process itself — the harness lists it in session/new's
// mcpServers as `migration-harness ask-user-mcp` — so the agent image needs
// nothing beyond the harness binary it already ships.
//
// Wire: newline-delimited JSON-RPC 2.0 on stdin/stdout (MCP stdio
// transport). Handled: initialize, notifications/initialized, ping,
// tools/list, tools/call; everything else answers method-not-found.
package askuser

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
)

// ToolName is the single tool this server exposes.
const ToolName = "ask_user"

// Subcommand is the harness CLI verb that runs this server.
const Subcommand = "ask-user-mcp"

// protocolVersion is the MCP revision this server speaks, and the only one
// initialize ever answers: elicitation/create — the whole point of this
// server — exists only from this revision, so agreeing to an older offer
// would promise a protocol we cannot keep. A client that cannot speak it
// disconnects at the handshake instead of failing on the first question
// mid-turn.
const protocolVersion = "2025-06-18"

// maxLine bounds one stdio frame (a tool call with a long question).
const maxLine = 4 << 20

// toolDescription is what the model reads when deciding to call the tool.
const toolDescription = "Ask the human watching this run a question and wait for the answer. " +
	"Use it whenever you need a decision only a human can make: a missing prerequisite, " +
	"an ambiguous requirement, a choice between approaches, or anything destructive. " +
	"The call blocks until someone answers. If nobody is watching, it says so — then " +
	"explain what you need and stop instead of guessing."

type rpcFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Server is one stdio MCP session.
type Server struct {
	out   io.Writer
	outMu sync.Mutex

	nextID  atomic.Int64
	pending map[string]chan rpcFrame // key: raw JSON id text
	pendMu  sync.Mutex

	logf func(format string, args ...any)
}

// New creates a server writing frames to out. logf (optional) receives
// diagnostics; pass nil to discard them.
func New(out io.Writer, logf func(format string, args ...any)) *Server {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Server{out: out, pending: make(map[string]chan rpcFrame), logf: logf}
}

// Serve reads frames from in until EOF or ctx is done. Tool calls run on
// their own goroutines so the reader keeps draining — the elicitation
// answer a call is waiting for arrives on the same stdin.
func (s *Server) Serve(ctx context.Context, in io.Reader) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLine)
	lines := make(chan []byte)
	errc := make(chan error, 1)
	go func() {
		defer close(lines)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			if len(strings.TrimSpace(string(line))) == 0 {
				continue
			}
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
		errc <- scanner.Err()
	}()

	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			s.failPending("server shutting down")
			return ctx.Err()
		case line, ok := <-lines:
			if !ok {
				s.failPending("stdin closed")
				select {
				case err := <-errc:
					return err
				default:
					return nil
				}
			}
			var f rpcFrame
			if err := json.Unmarshal(line, &f); err != nil {
				s.logf("askuser: malformed frame: %v", err)
				continue
			}
			switch {
			case f.Method == "" && len(f.ID) > 0:
				s.deliver(f)
			case f.Method != "" && len(f.ID) == 0:
				// Notifications (notifications/initialized, notifications/cancelled)
				// need no answer.
			case f.Method == "tools/call":
				wg.Add(1)
				go func() {
					defer wg.Done()
					s.handleToolCall(ctx, f)
				}()
			default:
				s.handleRequest(f)
			}
		}
	}
}

// ---------------------------------------------------------------- requests

func (s *Server) handleRequest(f rpcFrame) {
	switch f.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(f.Params, &p)
		if p.ProtocolVersion != protocolVersion {
			s.logf("askuser: client offered MCP protocol %q; answering %s, which elicitation requires", p.ProtocolVersion, protocolVersion)
		}
		s.respond(f.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "konveyor-ask-user", "version": "0.1.0"},
		})
	case "ping":
		s.respond(f.ID, map[string]any{})
	case "tools/list":
		s.respond(f.ID, map[string]any{"tools": []map[string]any{toolDefinition()}})
	default:
		s.respondError(f.ID, -32601, fmt.Sprintf("method not found: %s", f.Method))
	}
}

func toolDefinition() map[string]any {
	return map[string]any{
		"name":        ToolName,
		"description": toolDescription,
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{
					"type":        "string",
					"description": "The question, with enough context to answer it without reading the transcript.",
				},
				"options": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional fixed choices the human picks from. Omit for a free-text answer.",
				},
			},
			"required": []string{"question"},
		},
	}
}

// ToolCallArgs is the ask_user input.
type ToolCallArgs struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

// ElicitationSchema builds the ACP/MCP form schema for a question: one
// required string property "answer", an enum when options are given.
func ElicitationSchema(args ToolCallArgs) map[string]any {
	answer := map[string]any{
		"type":        "string",
		"title":       "Answer",
		"description": args.Question,
	}
	if len(args.Options) > 0 {
		answer["enum"] = args.Options
	}
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"answer": answer},
		"required":   []string{"answer"},
	}
}

func (s *Server) handleToolCall(ctx context.Context, f rpcFrame) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(f.Params, &call); err != nil {
		s.respondError(f.ID, -32602, "invalid tools/call params")
		return
	}
	if call.Name != ToolName {
		s.respondError(f.ID, -32602, fmt.Sprintf("unknown tool %q", call.Name))
		return
	}
	var args ToolCallArgs
	if len(call.Arguments) > 0 {
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			s.respondError(f.ID, -32602, "invalid ask_user arguments")
			return
		}
	}
	args.Question = strings.TrimSpace(args.Question)
	if args.Question == "" {
		s.toolResult(f.ID, "ask_user needs a question.", true)
		return
	}

	s.logf("askuser: asking %q (%d options)", args.Question, len(args.Options))
	reply, err := s.call(ctx, "elicitation/create", map[string]any{
		"message":         args.Question,
		"requestedSchema": ElicitationSchema(args),
	})
	s.toolResult(f.ID, AnswerText(reply, err), false)
}

// AnswerText turns an elicitation outcome into what the model reads.
func AnswerText(reply json.RawMessage, err error) string {
	if err != nil {
		return "No human answered (the question could not be delivered: " + err.Error() +
			"). Do not invent an answer: explain what you need and stop."
	}
	var r struct {
		Action  string                     `json:"action"`
		Content map[string]json.RawMessage `json:"content"`
	}
	_ = json.Unmarshal(reply, &r)
	switch r.Action {
	case "accept":
		var answer string
		if raw, ok := r.Content["answer"]; ok {
			if json.Unmarshal(raw, &answer) != nil {
				answer = string(raw)
			}
		}
		if strings.TrimSpace(answer) == "" {
			return "The human accepted the question but gave no answer. Decide for yourself if you safely can; otherwise explain what you need and stop."
		}
		return "The human answered: " + answer
	case "decline":
		return "The human declined to answer. Decide for yourself if you safely can; otherwise explain what you need and stop."
	default:
		return "No human answered (nobody is watching this run, or the question timed out). Do not invent an answer: explain what you need and stop."
	}
}

// ------------------------------------------------------------------ wire

func (s *Server) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := s.nextID.Add(1)
	key := fmt.Sprintf("%d", id)
	ch := make(chan rpcFrame, 1)
	s.pendMu.Lock()
	s.pending[key] = ch
	s.pendMu.Unlock()
	if err := s.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		s.pendMu.Lock()
		delete(s.pending, key)
		s.pendMu.Unlock()
		return nil, err
	}
	select {
	case f := <-ch:
		if f.Error != nil {
			return nil, fmt.Errorf("%s: %s (code %d)", method, f.Error.Message, f.Error.Code)
		}
		return f.Result, nil
	case <-ctx.Done():
		s.pendMu.Lock()
		delete(s.pending, key)
		s.pendMu.Unlock()
		return nil, ctx.Err()
	}
}

func (s *Server) deliver(f rpcFrame) {
	key := strings.Trim(string(f.ID), `"`)
	s.pendMu.Lock()
	ch, ok := s.pending[key]
	if ok {
		delete(s.pending, key)
	}
	s.pendMu.Unlock()
	if !ok {
		s.logf("askuser: response for unknown id %s", f.ID)
		return
	}
	ch <- f
}

func (s *Server) failPending(reason string) {
	s.pendMu.Lock()
	defer s.pendMu.Unlock()
	for key, ch := range s.pending {
		delete(s.pending, key)
		ch <- rpcFrame{Error: &rpcError{Code: -32603, Message: reason}}
	}
}

func (s *Server) toolResult(id json.RawMessage, text string, isError bool) {
	s.respond(id, map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	})
}

func (s *Server) respond(id json.RawMessage, result any) {
	if err := s.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result}); err != nil {
		s.logf("askuser: write response: %v", err)
	}
}

func (s *Server) respondError(id json.RawMessage, code int, msg string) {
	if err := s.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": rpcError{Code: code, Message: msg}}); err != nil {
		s.logf("askuser: write error: %v", err)
	}
}

func (s *Server) write(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	if _, err := s.out.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}
