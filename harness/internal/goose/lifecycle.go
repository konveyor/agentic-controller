package goose

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/konveyor/migration-harness/internal/logging"
)

// ServeProcess manages a goose serve process.
type ServeProcess struct {
	cmd       *exec.Cmd
	port      int
	secretKey string
	done      chan error
}

const (
	// DefaultACPPort is the standard port for goose serve in the Agentic
	// Platform. The controller and UI connect to this port for observability
	// and human-in-the-loop interaction.
	DefaultACPPort = 4000
)

// StartServe launches goose serve on the given port with authentication.
// If port is 0, DefaultACPPort (4000) is used. For local testing with
// multiple concurrent runs, pass a specific port or use FindFreePort().
// If KONVEYOR_ACP_SECRET_KEY is set in the environment, it is used.
// Otherwise a random key is generated for local testing.
func StartServe(ctx context.Context, port int) (*ServeProcess, error) {
	goosePath, err := exec.LookPath("goose")
	if err != nil {
		return nil, fmt.Errorf("goose not found: %w", err)
	}

	if port == 0 {
		port = DefaultACPPort
	}

	secretKey := os.Getenv("KONVEYOR_ACP_SECRET_KEY")
	if secretKey == "" {
		// REMOVE LATER: local testing only — in production the controller
		// provides KONVEYOR_ACP_SECRET_KEY via a K8s Secret.
		secretKey, err = generateLocalSecretKey()
		if err != nil {
			return nil, fmt.Errorf("generate secret key: %w", err)
		}
		logging.Warn("no KONVEYOR_ACP_SECRET_KEY set, generated local key for testing")
	}

	cmd := exec.CommandContext(ctx, goosePath, "serve",
		"--port", fmt.Sprintf("%d", port),
		"--with-builtin", "developer",
	)
	cmd.Env = append(os.Environ(), "GOOSE_SERVER__SECRET_KEY="+secretKey)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start goose serve: %w", err)
	}

	srv := &ServeProcess{
		cmd:       cmd,
		port:      port,
		secretKey: secretKey,
		done:      make(chan error, 1),
	}

	go func() {
		srv.done <- cmd.Wait()
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
func (s *ServeProcess) Stop() error {
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

// REMOVE LATER: local testing only — generates a random secret key
// when KONVEYOR_ACP_SECRET_KEY is not set.
func generateLocalSecretKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
