package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/konveyor/migration-harness/internal/logging"
	"github.com/konveyor/migration-harness/internal/params"
)

type lightspeedConfig struct {
	Cwd          string         `json:"cwd"`
	SystemPrompt string         `json:"system_prompt"`
	MaxTurns     int            `json:"max_turns"`
	Mode         string         `json:"mode"`
	AskUser      *AskUserConfig `json:"ask_user,omitempty"`
}

type lightspeedProcess struct {
	cmd       *exec.Cmd
	port      int
	secretKey string
	done      chan struct{}
	tempDir   string
	terminate func() error
}

func startLightspeed(ctx context.Context, cfg Config) (Process, error) {
	if cfg.Mode == "" {
		cfg.Mode = "auto"
	}
	if cfg.Mode != "auto" && cfg.Mode != "approve" {
		return nil, fmt.Errorf("unsupported Lightspeed mode %q", cfg.Mode)
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultACPPort
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		return nil, fmt.Errorf("Lightspeed python3 not found: %w", err)
	}
	dir, err := os.MkdirTemp("", "migration-harness-lightspeed-*")
	if err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(dir)
		}
	}()
	env, err := lightspeedEnv(os.Environ(), cfg, dir)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(lightspeedConfig{
		Cwd: cfg.Cwd, SystemPrompt: "", MaxTurns: params.NativeTurnLimit(cfg.MaxTurns), Mode: cfg.Mode, AskUser: cfg.AskUser,
	})
	if err != nil {
		return nil, err
	}
	configPath := filepath.Join(dir, "acp.json")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return nil, err
	}
	host := "127.0.0.1"
	cmd := exec.CommandContext(ctx, python, "-m", "lightspeed_agentic.acp", "--host", host, "--port", fmt.Sprint(cfg.Port), "--config", configPath)
	// Let the adapter close the active session and terminate local tool process
	// groups before os/exec falls back to killing the runtime process.
	var terminateOnce sync.Once
	var terminateErr error
	terminate := func() error {
		terminateOnce.Do(func() { terminateErr = cmd.Process.Signal(syscall.SIGTERM) })
		return terminateErr
	}
	cmd.Cancel = terminate
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = env
	// Runtime errors are returned with ACP error data; preserve process logs too.
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start Lightspeed ACP: %w", err)
	}
	srv := &lightspeedProcess{cmd: cmd, port: cfg.Port, secretKey: cfg.SecretKey, done: make(chan struct{}), tempDir: dir, terminate: terminate}
	go func() { cmd.Wait(); close(srv.done) }()
	cleanup = false
	logging.Info("Lightspeed ACP started on port %d (pid %d)", cfg.Port, cmd.Process.Pid)
	return srv, nil
}

func (s *lightspeedProcess) Port() int         { return s.port }
func (s *lightspeedProcess) SecretKey() string { return s.secretKey }
func (s *lightspeedProcess) Alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

// Lightspeed returns structured error details over ACP rather than Goose's
// unstructured provider-error log convention.
func (s *lightspeedProcess) ProviderError(_, _ time.Time, _ time.Duration) string { return "" }
func (s *lightspeedProcess) Stop() error {
	defer os.RemoveAll(s.tempDir)
	if !s.Alive() {
		return nil
	}
	if err := s.terminate(); err != nil {
		return fmt.Errorf("sigterm Lightspeed: %w", err)
	}
	select {
	case <-s.done:
		return nil
	case <-time.After(5 * time.Second):
		if err := s.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("sigkill Lightspeed: %w", err)
		}
		<-s.done
		return nil
	}
}

// lightspeedEnv maps Gateway configuration to the runtime's existing typed
// provider resolver. Inspection, admission, MCP and TLS settings pass through.
// Credential files stay outside the checkout and disappear on process cleanup.
func lightspeedEnv(base []string, cfg Config, dir string) ([]string, error) {
	values := map[string]string{}
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	// Defense in depth: Hub preparation already clears these from the harness
	// environment; never reintroduce a token that can resolve push credentials.
	for _, key := range []string{"HUB_BASE_URL", "HUB_TOKEN", "HUB_TOKEN_ID", "APP_ID"} {
		delete(values, key)
	}
	provider := strings.ReplaceAll(strings.ToLower(cfg.Provider), "_", "-")
	switch provider {
	case "anthropic", "":
		values["LIGHTSPEED_PROVIDER"] = "anthropic"
		if cfg.APIKey != "" {
			values["ANTHROPIC_API_KEY"] = cfg.APIKey
		}
	case "openai":
		values["LIGHTSPEED_PROVIDER"] = "openai"
		if cfg.APIKey != "" {
			values["OPENAI_API_KEY"] = cfg.APIKey
		}
	case "aws-bedrock":
		values["LIGHTSPEED_PROVIDER"] = "bedrock"
	case "gcp-vertex-ai":
		values["LIGHTSPEED_PROVIDER"] = "vertex"
		family := "google"
		if strings.Contains(strings.ToLower(cfg.Model), "claude") {
			family = "anthropic"
		}
		values["LIGHTSPEED_MODEL_PROVIDER"] = family
		if values["LIGHTSPEED_PROVIDER_PROJECT"] == "" {
			values["LIGHTSPEED_PROVIDER_PROJECT"] = values["GCP_PROJECT_ID"]
			if values["LIGHTSPEED_PROVIDER_PROJECT"] == "" {
				values["LIGHTSPEED_PROVIDER_PROJECT"] = values["GOOGLE_CLOUD_PROJECT"]
			}
		}
		if values["LIGHTSPEED_PROVIDER_REGION"] == "" {
			values["LIGHTSPEED_PROVIDER_REGION"] = values["GCP_LOCATION"]
			if values["LIGHTSPEED_PROVIDER_REGION"] == "" {
				values["LIGHTSPEED_PROVIDER_REGION"] = values["GOOGLE_CLOUD_LOCATION"]
			}
		}
		// The runtime's Vertex resolver reads this specific filename.
		content := values["GOOGLE_APPLICATION_CREDENTIALS_JSON"]
		if content != "" {
			if err := os.WriteFile(filepath.Join(dir, "GOOGLE_APPLICATION_CREDENTIALS"), []byte(content), 0600); err != nil {
				return nil, fmt.Errorf("write Vertex credentials: %w", err)
			}
			values["LIGHTSPEED_LLM_CREDENTIALS_PATH"] = dir
		}
		delete(values, "GOOGLE_APPLICATION_CREDENTIALS_JSON")
	default:
		return nil, fmt.Errorf("Gateway provider %q is unsupported by the Lightspeed runtime (use anthropic, openai, aws-bedrock, or gcp-vertex-ai)", cfg.Provider)
	}
	values["LIGHTSPEED_MODEL"] = cfg.Model
	values["LIGHTSPEED_PROVIDER_URL"] = cfg.Endpoint
	values["LIGHTSPEED_ACP_SECRET_KEY"] = cfg.SecretKey
	if values["LIGHTSPEED_AGENT_TIMEOUT_SECONDS"] == "" {
		values["LIGHTSPEED_AGENT_TIMEOUT_SECONDS"] = "3600"
	}
	env := make([]string, 0, len(values))
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env, nil
}
