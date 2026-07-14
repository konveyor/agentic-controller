package goose

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/konveyor/migration-harness/internal/acp"
	"github.com/konveyor/migration-harness/internal/logging"
)

// ACPRunner implements the Runner interface using goose serve (ACP over
// WebSocket) instead of goose run (CLI). The existing CLIRunner stays
// as fallback. All pipeline steps (plan, execute, verify, fixloop)
// work without modification — they call RunRecipe the same way.
type ACPRunner struct {
	session  *acp.SessionClient
	serve    *ServeProcess
	provider string
	model    string
	logDir   string
	cwd      string
}

// NewACPRunner creates a runner backed by a goose serve WebSocket connection.
func NewACPRunner(session *acp.SessionClient, serve *ServeProcess, provider, model, logDir, cwd string) *ACPRunner {
	return &ACPRunner{
		session:  session,
		serve:    serve,
		provider: provider,
		model:    model,
		logDir:   logDir,
		cwd:      cwd,
	}
}

// RunRecipe implements the Runner interface. It parses the recipe YAML,
// renders templates, creates an ACP session, sends the prompt, and
// extracts structured output — producing the same json.RawMessage
// shape that CLIRunner returns.
func (r *ACPRunner) RunRecipe(ctx context.Context, recipe string, maxTurns int, params map[string]string) (json.RawMessage, error) {
	if !r.serve.Alive() {
		return nil, fmt.Errorf("goose serve is not running")
	}

	recipeName := strings.TrimSuffix(filepath.Base(recipe), ".yaml")
	logging.Info("ACP: running recipe %s (max %d turns)", recipeName, maxTurns)

	// Parse and render the recipe. For dynamically generated recipes
	// (like the plan recipe), the file is already a rendered YAML —
	// params may be nil/empty.
	rec, err := ParseRecipe(recipe)
	if err != nil {
		return nil, fmt.Errorf("parse recipe: %w", err)
	}

	if params == nil {
		params = map[string]string{}
	}

	instructions, prompt, err := rec.Render(params)
	if err != nil {
		return nil, fmt.Errorf("render recipe: %w", err)
	}

	// Build the full prompt with schema
	fullPrompt, err := rec.BuildACPPrompt(instructions, prompt)
	if err != nil {
		return nil, fmt.Errorf("build ACP prompt: %w", err)
	}

	// Add max turns guidance to the prompt
	if maxTurns > 0 {
		fullPrompt += fmt.Sprintf("\n\nYou have a maximum of %d tool calls for this task.", maxTurns)
	}

	// Create a fresh session for this recipe call (matches CLIRunner
	// behavior — each RunRecipe is independent, no shared context)
	sessionID, err := r.session.CreateSession(ctx, r.cwd, rec.MCPServers())
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	// Send the prompt and collect streaming response
	result, err := r.session.SendPrompt(ctx, sessionID, []acp.ContentBlock{
		{Type: "text", Text: fullPrompt},
	})
	if err != nil {
		return nil, fmt.Errorf("send prompt: %w", err)
	}

	// Log the full response
	logPath := filepath.Join(r.logDir, fmt.Sprintf("%s-%d.json", recipeName, os.Getpid()))
	logData, _ := json.MarshalIndent(result, "", "  ")
	os.WriteFile(logPath, logData, 0644)

	// Check stop reason
	switch result.StopReason {
	case "end_turn":
		// normal completion
	case "max_tokens":
		return nil, fmt.Errorf("model hit context limit — try a model with a larger context window")
	case "max_turn_requests":
		return nil, fmt.Errorf("hit max tool calls (%d) — increase max turns", maxTurns)
	case "refusal":
		return nil, fmt.Errorf("model refused the task")
	default:
		logging.Warn("unexpected stop reason: %s", result.StopReason)
	}

	// Extract structured output from the collected message chunks
	output, err := extractStructuredOutput(result.Chunks)
	if err != nil {
		return nil, fmt.Errorf("extract output: %w", err)
	}

	return output, nil
}

// extractStructuredOutput attempts to find JSON in the agent's response.
// The agent may return JSON directly, wrapped in markdown code blocks,
// or mixed with other text.
func extractStructuredOutput(chunks []string) (json.RawMessage, error) {
	if len(chunks) == 0 {
		return nil, fmt.Errorf("no response chunks received")
	}

	full := strings.Join(chunks, "")

	// Try parsing the full text as JSON directly
	if isJSON(full) {
		return json.RawMessage(full), nil
	}

	// Try extracting from markdown code blocks: ```json ... ```
	if extracted := extractFromCodeBlock(full); extracted != "" {
		if isJSON(extracted) {
			return json.RawMessage(extracted), nil
		}
	}

	// Try finding JSON object anywhere in the text
	if extracted := extractJSONObject(full); extracted != "" {
		return json.RawMessage(extracted), nil
	}

	return nil, fmt.Errorf("no valid JSON found in agent response (%d bytes)", len(full))
}

func isJSON(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return false
	}
	var js json.RawMessage
	return json.Unmarshal([]byte(s), &js) == nil
}

func extractFromCodeBlock(s string) string {
	markers := []string{"```json\n", "```JSON\n", "```\n"}
	for _, start := range markers {
		idx := strings.Index(s, start)
		if idx < 0 {
			continue
		}
		content := s[idx+len(start):]
		end := strings.Index(content, "```")
		if end < 0 {
			continue
		}
		return strings.TrimSpace(content[:end])
	}
	return ""
}

func extractJSONObject(s string) string {
	// Search from the END of the text — the structured output is
	// typically the last JSON object in the response. Searching from
	// the start would match JSON embedded in PLAN.md code examples.
	for start := strings.LastIndex(s, "{"); start >= 0; start = strings.LastIndex(s[:start], "{") {
		depth := 0
		for i := start; i < len(s); i++ {
			switch s[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					candidate := s[start : i+1]
					if isJSON(candidate) {
						return candidate
					}
					break
				}
			}
		}
	}
	return ""
}
