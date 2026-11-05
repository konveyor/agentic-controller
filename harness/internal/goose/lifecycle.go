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
	"github.com/konveyor/migration-harness/internal/params"
)

// ServeProcess manages a goose serve process.
type ServeProcess struct {
	cmd       *exec.Cmd
	port      int
	secretKey string
	done      chan struct{}
	tempDirs  []string
	output    *outputCapture
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
	// Mode sets GOOSE_MODE (auto/approve); empty defaults to "auto"
	// (ADR 0011).
	Mode string
	// MaxTurns is the configured (not yet reserved) turn budget.
	// StartServe reserves params.ReserveFraction of it for the
	// harness's wind-down handoff prompt when setting GOOSE_MAX_TURNS
	// (ADR 0011) — 0 means unset (runtime default applies).
	MaxTurns int
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
	env, tempDirs := providerEnv(os.Environ(), provider, model, apiKey, endpoint)
	env = append(env, executionEnv(cfg.Mode, cfg.MaxTurns)...)
	if secretKey != "" {
		env = append(env, "GOOSE_SERVER__SECRET_KEY="+secretKey)
	}
	cmd.Env = env
	// One capture across both streams, so the order the process wrote its
	// lines in is the order they are read back. Everything still reaches
	// the pod log exactly as before; the capture only keeps a copy of the
	// error lines, for ProviderError (#323).
	output := newOutputCapture(os.Stderr)
	cmd.Stdout = output
	cmd.Stderr = output

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
		output:    output,
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

// ProviderError returns the error line goose logged for the model call
// that just failed, or "" when it logged none. Callers use it to name why
// the call failed when the ACP error itself does not (#323).
//
// failedAt is when the call came back failed, and the line that explains
// it is logged within a moment of that — so within bounds the search in
// both directions: how long to wait for a line still in flight, and how
// far back a line may already have been read. notBefore is a hard floor
// on top of that, so nothing from before this prompt is ever reported.
//
// The wait is there because the two paths race: the JSON-RPC error
// arrives over the websocket, while goose's stdio reaches the harness
// through os/exec's copier goroutine, so the response is often handled
// before the line it explains has been read.
//
// What the wait must not do is end early on a line that was already
// there. A stage prompt runs many turns, and goose logs an error for a
// recoverable tool failure too; returning one of those the moment the
// call fails would hand the viewer and the termination log an error from
// earlier in the run as the reason for this one, without ever waiting for
// the real one racing over stdio. So only a line read after the failure
// ends the wait early — the provider's own text is logged before goose's
// wrapping of it, which makes the first such line the most specific one
// available. Otherwise the full grace is spent, and the newest line
// inside the window is the answer, "" included. That costs a second on a
// run that has already failed, and buys an answer that is about the
// failure.
func (s *ServeProcess) ProviderError(notBefore, failedAt time.Time, within time.Duration) string {
	if s.output == nil {
		return ""
	}
	floor := failedAt.Add(-within)
	if floor.Before(notBefore) {
		floor = notBefore
	}
	deadline := time.Now().Add(within)
	for {
		line, at := s.output.lastErrorSince(floor)
		if line != "" && at.After(failedAt) {
			return line
		}
		if !time.Now().Before(deadline) {
			return line
		}
		time.Sleep(providerErrorPollInterval)
	}
}

// ProviderErrorGrace is how long ProviderError should be given to pick up
// the line explaining a failure the ACP error left unexplained, and how
// far before the failure a line may have been read; providerErrorPollInterval
// is how often it looks. A second is far longer than the copier goroutine
// needs, and short enough that a recoverable error from earlier in the
// stage is not mistaken for the reason this call failed.
const (
	ProviderErrorGrace        = time.Second
	providerErrorPollInterval = 20 * time.Millisecond
)

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

// providerEnv returns base with the LLM provider credentials translated
// to the env vars goose expects. Called before starting goose serve so
// the process has the right credentials at startup. In a Sandbox, the
// controller injects KONVEYOR_LLM_* env vars (with
// KONVEYOR_MODEL_PRIMARY_* as legacy fallbacks); this function maps them
// to provider-specific names.
//
// base is the environment to build on — os.Environ() in production, so
// goose inherits PATH, HOME and the rest. It is a parameter rather than
// a call to os.Environ() inside, because a test asking "does the harness
// add this provider's key" must be able to ask about what this function
// appended and not about what the developer happens to have exported.
// base is copied, never appended to in place.
func providerEnv(base []string, provider, model, apiKey, endpoint string) (env []string, tempDirs []string) {
	env = append([]string(nil), base...)
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
		case "xai":
			env = append(env, "XAI_API_KEY="+apiKey)
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
		case "xai":
			// goose defaults XAI_HOST to https://api.x.ai/v1; the Gateway
			// endpoint (https://api.x.ai) is used only for the controller's
			// /v1/models verification probe, so it is not forwarded here.
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

// executionEnv translates execution controls into the env vars goose
// expects (ADR 0011). GOOSE_MODE is always set (default "auto" when
// mode is empty, so behavior is explicit even without a params.json
// execution.mode value). GOOSE_MAX_TURNS is set only when maxTurns > 0,
// reserving params.ReserveFraction of it for the harness's wind-down
// handoff prompt — the runtime is the sole turn counter now (ADR 0011).
func executionEnv(mode string, maxTurns int) []string {
	if mode == "" {
		mode = "auto"
	}
	env := []string{"GOOSE_MODE=" + mode}
	if native := params.NativeTurnLimit(maxTurns); native > 0 {
		env = append(env, fmt.Sprintf("GOOSE_MAX_TURNS=%d", native))
	}
	return env
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
