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
	"time"

	"github.com/konveyor/migration-harness/internal/logging"
)

type Runner interface {
	RunRecipe(ctx context.Context, recipe string, maxTurns int, params map[string]string) (json.RawMessage, error)
}

type CLIRunner struct {
	Provider string
	Model    string
	Endpoint string
	APIKey   string
	LogDir   string
}

func NewCLIRunner(provider, model, logDir string) *CLIRunner {
	return &CLIRunner{Provider: provider, Model: model, LogDir: logDir}
}

func NewCLIRunnerFull(provider, model, endpoint, apiKey, logDir string) *CLIRunner {
	return &CLIRunner{
		Provider: provider,
		Model:    model,
		Endpoint: endpoint,
		APIKey:   apiKey,
		LogDir:   logDir,
	}
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
	logPath := filepath.Join(r.LogDir, fmt.Sprintf("%s-%d-%d.json", name, os.Getpid(), time.Now().UnixNano()))

	cmd := exec.CommandContext(ctx, goosePath, args...)
	cmd.Env = providerEnv(r.Provider, r.APIKey, r.Endpoint)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	logging.Info("goose run --recipe %s (max %d turns)", filepath.Base(recipe), maxTurns)

	runErr := cmd.Run()

	raw := stdout.Bytes()

	if len(raw) > 0 {
		if err := os.WriteFile(logPath, raw, 0644); err != nil {
			logging.Warn("write goose log %s: %v", logPath, err)
		}
	}

	if stderrOut := stderr.String(); stderrOut != "" {
		logging.Warn("goose stderr: %s", stderrOut)
	}

	if runErr != nil {
		logging.Warn("goose exited with error: %v", runErr)
		if len(raw) == 0 {
			return nil, fmt.Errorf("goose failed with no output: %w", runErr)
		}
	} else if len(raw) == 0 {
		return nil, fmt.Errorf("goose produced no output")
	}

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

// providerEnv returns the current process environment with credentials
// set in the provider-specific env vars that Goose expects.
//
// Goose env var reference:
//
//	Anthropic:    ANTHROPIC_API_KEY, ANTHROPIC_HOST
//	OpenAI:       OPENAI_API_KEY, OPENAI_HOST
//	Google:       GOOGLE_API_KEY
//	GCP Vertex:   GOOGLE_APPLICATION_CREDENTIALS (ADC), GCP_PROJECT_ID, GCP_LOCATION
func providerEnv(provider, apiKey, endpoint string) []string {
	env := os.Environ()

	p := strings.ToLower(provider)

	if apiKey != "" {
		switch p {
		case "anthropic":
			env = append(env, "ANTHROPIC_API_KEY="+apiKey)
		case "openai":
			env = append(env, "OPENAI_API_KEY="+apiKey)
		case "google":
			env = append(env, "GOOGLE_API_KEY="+apiKey)
		case "gcp_vertex_ai":
			path, err := writeADCFile(apiKey)
			if err != nil {
				logging.Warn("write ADC file: %v", err)
			} else {
				env = append(env, "GOOGLE_APPLICATION_CREDENTIALS="+path)
			}
		}
	}

	if endpoint != "" {
		switch p {
		case "anthropic":
			env = append(env, "ANTHROPIC_HOST="+endpoint)
		case "openai":
			env = append(env, "OPENAI_HOST="+endpoint)
		}
	}

	return env
}

// writeADCFile writes the service account JSON to a file for Google
// ADC. Goose reads credentials from a file path, not inline. Uses
// $HOME/.migration-harness/ since /tmp may not be writable in containers.
func writeADCFile(content string) (string, error) {
	dir := filepath.Join(os.Getenv("HOME"), ".migration-harness")
	os.MkdirAll(dir, 0700)
	path := filepath.Join(dir, "gcp-adc.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return "", fmt.Errorf("write ADC file: %w", err)
	}
	return path, nil
}

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
