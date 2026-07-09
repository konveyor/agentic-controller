package goose

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/konveyor/migration-harness/internal/logging"
)

type Runner interface {
	RunRecipe(ctx context.Context, recipe string, maxTurns int, params map[string]string) (json.RawMessage, error)
}

type CLIRunner struct {
	Provider string
	Model    string
	LogDir   string
}

func NewCLIRunner(provider, model, logDir string) *CLIRunner {
	return &CLIRunner{Provider: provider, Model: model, LogDir: logDir}
}

func (r *CLIRunner) RunRecipe(ctx context.Context, recipe string, maxTurns int, params map[string]string) (json.RawMessage, error) {
	goosePath, err := exec.LookPath("goose")
	if err != nil {
		return nil, fmt.Errorf("goose not found: %w", err)
	}

	args := []string{
		"run",
		"--recipe", recipe,
		"--no-session",
		"--quiet",
		"--output-format", "json",
		"--max-turns", fmt.Sprintf("%d", maxTurns),
		"--provider", r.Provider,
		"--model", r.Model,
	}

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--params", fmt.Sprintf("%s=%s", k, params[k]))
	}

	name := strings.TrimSuffix(filepath.Base(recipe), ".yaml")
	logPath := filepath.Join(r.LogDir, fmt.Sprintf("%s-%d.json", name, os.Getpid()))

	cmd := exec.CommandContext(ctx, goosePath, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr

	logging.Info("goose run --recipe %s (max %d turns)", filepath.Base(recipe), maxTurns)

	err = cmd.Run()
	if err != nil {
		logging.Warn("goose exited with error: %v", err)
	}

	raw := stdout.Bytes()
	if len(raw) == 0 {
		return nil, fmt.Errorf("goose produced no output")
	}

	os.WriteFile(logPath, raw, 0644)

	clean := StripBanner(raw)

	if err := CheckAPIErrors(clean); err != nil {
		return nil, err
	}

	result, err := ExtractFinalOutput(clean)
	if err != nil {
		return nil, fmt.Errorf("extract final output: %w", err)
	}

	return result, nil
}

// StripBanner removes any text before the first '{' in goose output.
// Goose sometimes prints ASCII art banners before the JSON conversation.
func StripBanner(data []byte) []byte {
	idx := bytes.IndexByte(data, '{')
	if idx < 0 {
		return data
	}
	return data[idx:]
}

// CheckAPIErrors scans goose output for billing/quota errors.
func CheckAPIErrors(data []byte) error {
	s := string(data)
	errorPatterns := []string{
		"credit balance is too low",
		"rate limit",
		"quota exceeded",
	}
	lower := strings.ToLower(s)
	for _, p := range errorPatterns {
		if strings.Contains(lower, p) {
			return fmt.Errorf("API error: %s", p)
		}
	}
	return nil
}

// ExtractFinalOutput extracts the recipe__final_output from a goose
// conversation JSON. This mirrors the jq expression from common.sh:
//
//	.messages[].content[]
//	  | select(.type == "toolRequest" and .toolCall.value.name == "recipe__final_output")
//	  | .toolCall.value.arguments
func ExtractFinalOutput(data []byte) (json.RawMessage, error) {
	var conv struct {
		Messages []struct {
			Content []json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &conv); err != nil {
		return nil, fmt.Errorf("parse conversation: %w", err)
	}

	var last json.RawMessage
	for _, msg := range conv.Messages {
		for _, content := range msg.Content {
			var item struct {
				Type     string `json:"type"`
				ToolCall struct {
					Value struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"value"`
				} `json:"toolCall"`
			}
			if err := json.Unmarshal(content, &item); err != nil {
				continue
			}
			if item.Type == "toolRequest" && item.ToolCall.Value.Name == "recipe__final_output" {
				last = item.ToolCall.Value.Arguments
			}
		}
	}

	if last == nil {
		return nil, fmt.Errorf("no recipe__final_output found in goose output")
	}
	return last, nil
}
