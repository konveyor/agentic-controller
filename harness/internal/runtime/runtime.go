// Package runtime selects an ACP runtime without owning the harness lifecycle.
package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/konveyor/migration-harness/internal/goose"
)

const (
	DefaultACPPort     = goose.DefaultACPPort
	LoopbackACPPort    = goose.LoopbackACPPort
	ProviderErrorGrace = goose.ProviderErrorGrace
)

// Process is the ACP server consumed by the existing session client and tee.
type Process interface {
	Port() int
	SecretKey() string
	Alive() bool
	Stop() error
	ProviderError(notBefore, failedAt time.Time, within time.Duration) string
}

// Config carries runtime configuration, never Hub or Git credentials.
type Config struct {
	Name         string
	Port         int
	BindLoopback bool
	SecretKey    string
	Provider     string
	Model        string
	APIKey       string
	Endpoint     string
	Mode         string
	MaxTurns     int
	MaxCost      float64
	Cwd          string
	AskUser      *AskUserConfig
}

// AskUserConfig pins the trusted harness MCP executable before session/new.
type AskUserConfig struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// Validate rejects unsupported budgets before any model can spend them. The
// reusable Lightspeed runtime currently has token usage, but no monetary source.
func Validate(name string, maxCost float64, acpTee bool) error {
	if name != "" && name != "goose" && name != "lightspeed" {
		return fmt.Errorf("unsupported runtime %q", name)
	}
	if name == "lightspeed" && maxCost > 0 {
		return fmt.Errorf("execution.maxCost is unsupported by the Lightspeed prototype: monetary usage is unavailable, so the harness cannot enforce this budget; omit maxCost to run with token usage reporting")
	}
	if name == "lightspeed" && !acpTee {
		return fmt.Errorf("HARNESS_ACP_TEE=off is unsupported by the Lightspeed prototype: its ACP adapter binds loopback and requires the harness viewer tee")
	}
	return nil
}

func Start(ctx context.Context, cfg Config) (Process, error) {
	if err := Validate(cfg.Name, cfg.MaxCost, cfg.BindLoopback); err != nil {
		return nil, err
	}
	switch cfg.Name {
	case "", "goose":
		return goose.StartServe(ctx, goose.ServeConfig{
			Port: cfg.Port, BindLoopback: cfg.BindLoopback, SecretKey: cfg.SecretKey,
			Provider: cfg.Provider, Model: cfg.Model, APIKey: cfg.APIKey, Endpoint: cfg.Endpoint,
			Mode: cfg.Mode, MaxTurns: cfg.MaxTurns,
		})
	case "lightspeed":
		return startLightspeed(ctx, cfg)
	default:
		return nil, fmt.Errorf("unsupported runtime %q", cfg.Name)
	}
}
