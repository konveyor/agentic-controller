package goose

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/konveyor/migration-harness/internal/logging"
)

// ServeProcess manages a goose serve process.
type ServeProcess struct {
	cmd       *exec.Cmd
	port      int
	secretKey string
	done      chan struct{}
	tempDirs  []string
}

const (
	// DefaultACPPort is the standard port for the pod's ACP endpoint in
	// the Agentic Platform. The controller and UI connect to this port
	// for observability and human-in-the-loop interaction. With the ACP
	// tee enabled (the default) the harness owns this port and goose
	// serves on LoopbackACPPort behind it.
	DefaultACPPort = 4000

	// LoopbackACPPort is where goose serve binds when the harness tee
	// fronts it on DefaultACPPort. Loopback-only by construction: the
	// pod's external ACP surface is the tee.
	LoopbackACPPort = 4001
)

// StartServe launches goose serve per cfg. Takes a struct rather than a
// positional list: the parameters are a clump of same-typed strings, and
// a miscount is silent at compile time.
//
// With cfg.BindLoopback the server stays on 127.0.0.1 regardless of key —
// used when the harness ACP tee is the pod's external endpoint and goose
// must not be reachable off-host directly. Otherwise bind address follows
// authentication: with a secret key the server binds all interfaces — in
// a Sandbox the platform attaches to <pod>:4000 through the run's
// headless Service, which goose's default loopback bind would refuse.
// Without a key (bare CLI use) it stays loopback-only; an unauthenticated
// ACP server must never be reachable off-host.
// ServeConfig configures StartServe. The zero value is a bare,
// unauthenticated loopback server on DefaultACPPort — enough for CLI use
// and tests; a Sandbox sets SecretKey and the model fields.
type ServeConfig struct {
	// Port to listen on; 0 means DefaultACPPort.
	Port int
	// BindLoopback keeps goose on 127.0.0.1 even with a SecretKey set —
	// the harness ACP tee is then the pod's external endpoint.
	BindLoopback bool
	// SecretKey is the ACP authentication key (config.Config.ACPSecretKey).
	SecretKey string
	// Provider, Model, APIKey and Endpoint are translated to the
	// provider-specific env vars goose expects.
	Provider string
	Model    string
	APIKey   string
	Endpoint string
}

func StartServe(ctx context.Context, cfg ServeConfig) (*ServeProcess, error) {
	port, bindLoopback, secretKey := cfg.Port, cfg.BindLoopback, cfg.SecretKey
	provider, model, apiKey, endpoint := cfg.Provider, cfg.Model, cfg.APIKey, cfg.Endpoint
	goosePath, err := exec.LookPath("goose")
	if err != nil {
		return nil, fmt.Errorf("goose not found: %w", err)
	}

	if port == 0 {
		port = DefaultACPPort
	}

	host := "127.0.0.1"
	if secretKey != "" && !bindLoopback {
		host = "0.0.0.0"
	}

	cmd := exec.CommandContext(ctx, goosePath, "serve",
		"--host", host,
		"--port", fmt.Sprintf("%d", port),
		"--with-builtin", "developer",
	)
	env, tempDirs := providerEnv(provider, model, apiKey, endpoint)
	if secretKey != "" {
		env = append(env, "GOOSE_SERVER__SECRET_KEY="+secretKey)
	}
	cmd.Env = env
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		for _, d := range tempDirs {
			os.RemoveAll(d)
		}
		return nil, fmt.Errorf("start goose serve: %w", err)
	}

	srv := &ServeProcess{
		cmd:       cmd,
		port:      port,
		secretKey: secretKey,
		done:      make(chan struct{}),
		tempDirs:  tempDirs,
	}

	go func() {
		cmd.Wait()
		close(srv.done)
	}()

	logging.Info("goose serve started on port %d (pid %d)", port, cmd.Process.Pid)
	return srv, nil
}

// Port returns the port goose serve is listening on.
func (s *ServeProcess) Port() int {
	return s.port
}

// SecretKey returns the ACP secret key for client authentication.
func (s *ServeProcess) SecretKey() string {
	return s.secretKey
}

// Alive returns true if the goose serve process is still running.
func (s *ServeProcess) Alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

// Stop sends SIGTERM and waits up to 5 seconds, then SIGKILL.
// Cleans up any temporary credential files created during startup.
func (s *ServeProcess) Stop() error {
	defer s.cleanup()

	if !s.Alive() {
		return nil
	}

	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("sigterm: %w", err)
	}

	select {
	case <-s.done:
		logging.Ok("goose serve stopped cleanly")
		return nil
	case <-time.After(5 * time.Second):
		logging.Warn("goose serve did not stop in 5s, sending SIGKILL")
		if err := s.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("sigkill: %w", err)
		}
		<-s.done
		return nil
	}
}

func (s *ServeProcess) cleanup() {
	for _, d := range s.tempDirs {
		os.RemoveAll(d)
	}
	s.tempDirs = nil
}

// FindFreePort returns an available TCP port.
func FindFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}

// providerEnv returns the current process environment with LLM provider
// credentials translated to the env vars goose expects. Called before
// starting goose serve so the process has the right credentials at
// startup. In a Sandbox, the controller injects KONVEYOR_LLM_* env vars
// (with KONVEYOR_MODEL_PRIMARY_* as legacy fallbacks); this function
// maps them to provider-specific names.
func providerEnv(provider, model, apiKey, endpoint string) (env []string, tempDirs []string) {
	env = os.Environ()
	p := strings.ReplaceAll(strings.ToLower(provider), "-", "_")

	if p != "" {
		env = append(env, "GOOSE_PROVIDER="+p)
	}
	if model != "" {
		env = append(env, "GOOSE_MODEL="+model)
	}

	if apiKey != "" {
		switch p {
		case "anthropic":
			env = append(env, "ANTHROPIC_API_KEY="+apiKey)
		case "openai":
			env = append(env, "OPENAI_API_KEY="+apiKey)
		case "google":
			env = append(env, "GOOGLE_API_KEY="+apiKey)
		case "gcp_vertex_ai":
			// uses ADC credentials, not an API key
		case "aws_bedrock":
			// uses AWS SDK credentials, not an API key
		default:
			logging.Warn("unmapped provider %q — API key not forwarded to goose", p)
		}
	}

	if p == "gcp_vertex_ai" {
		content := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_JSON")
		if content != "" {
			path, err := writeADCFile(content)
			if err != nil {
				logging.Warn("write ADC file: %v", err)
			} else {
				env = append(env, "GOOGLE_APPLICATION_CREDENTIALS="+path)
				tempDirs = append(tempDirs, filepath.Dir(path))
			}
			env = filterEnvKey(env, "GOOGLE_APPLICATION_CREDENTIALS_JSON")
		}
	}

	if endpoint != "" {
		switch p {
		case "anthropic":
			env = append(env, "ANTHROPIC_HOST="+endpoint)
		case "openai":
			env = append(env, "OPENAI_HOST="+endpoint)
		case "gcp_vertex_ai":
			// endpoint configured via ADC project/region, not env var
		case "aws_bedrock":
			// endpoint derived from AWS_REGION by the AWS SDK
		default:
			logging.Warn("unmapped provider %q — endpoint not forwarded to goose", p)
		}
	}

	return env, tempDirs
}

func filterEnvKey(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return out
}

// writeADCFile writes service account JSON to a temp file for Google ADC.
// Uses a temp directory outside the repo to prevent accidental commit/push.
func writeADCFile(content string) (string, error) {
	dir, err := os.MkdirTemp("", "migration-harness-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir for ADC: %w", err)
	}
	path := filepath.Join(dir, "gcp-adc.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return "", fmt.Errorf("write ADC file: %w", err)
	}
	return path, nil
}
