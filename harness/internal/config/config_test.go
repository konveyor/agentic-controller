package config

import (
	"os"
	"testing"
)

func clearKonveyorEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"KONVEYOR_MODEL_PRIMARY_MODEL",
		"KONVEYOR_MODEL_PRIMARY_PROVIDER",
		"KONVEYOR_MODEL_PRIMARY_ENDPOINT",
		"KONVEYOR_MODEL_PRIMARY_API_KEY",
		"KONVEYOR_PARAM_MAX_TURNS",
		"KONVEYOR_PARAM_MAX_FIX_ITERATIONS",
		"HUB_BASE_URL",
		"HUB_TOKEN",
		"APP_ID",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func setHubEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HUB_BASE_URL", "https://hub.example.com")
	t.Setenv("APP_ID", "42")
}

func TestLoadFromEnv(t *testing.T) {
	t.Run("returns config from env", func(t *testing.T) {
		clearKonveyorEnv(t)
		setHubEnv(t)
		t.Setenv("KONVEYOR_MODEL_PRIMARY_MODEL", "claude-sonnet-4-5")
		t.Setenv("KONVEYOR_MODEL_PRIMARY_PROVIDER", "anthropic")
		t.Setenv("KONVEYOR_MODEL_PRIMARY_ENDPOINT", "https://api.anthropic.com")
		t.Setenv("KONVEYOR_MODEL_PRIMARY_API_KEY", "sk-test-key")

		cfg := LoadFromEnv()
		if cfg == nil {
			t.Fatal("expected config, got nil")
		}
		if cfg.Model != "claude-sonnet-4-5" {
			t.Errorf("Model = %q, want %q", cfg.Model, "claude-sonnet-4-5")
		}
		if cfg.Provider != "anthropic" {
			t.Errorf("Provider = %q, want %q", cfg.Provider, "anthropic")
		}
		if cfg.Endpoint != "https://api.anthropic.com" {
			t.Errorf("Endpoint = %q, want %q", cfg.Endpoint, "https://api.anthropic.com")
		}
		if cfg.APIKey != "sk-test-key" {
			t.Errorf("APIKey = %q, want %q", cfg.APIKey, "sk-test-key")
		}
		if cfg.MaxTurns != DefaultMaxTurns {
			t.Errorf("MaxTurns = %d, want default %d", cfg.MaxTurns, DefaultMaxTurns)
		}
	})

	t.Run("returns nil when env not set", func(t *testing.T) {
		clearKonveyorEnv(t)

		cfg := LoadFromEnv()
		if cfg != nil {
			t.Fatalf("expected nil, got %+v", cfg)
		}
	})

	t.Run("reads optional param overrides", func(t *testing.T) {
		setHubEnv(t)
		t.Setenv("KONVEYOR_MODEL_PRIMARY_MODEL", "gemini-2.5-pro")
		t.Setenv("KONVEYOR_MODEL_PRIMARY_PROVIDER", "gcp_vertex_ai")
		t.Setenv("KONVEYOR_PARAM_MAX_TURNS", "500")
		t.Setenv("KONVEYOR_PARAM_MAX_FIX_ITERATIONS", "7")

		cfg := LoadFromEnv()
		if cfg == nil {
			t.Fatal("expected config, got nil")
		}
		if cfg.MaxTurns != 500 {
			t.Errorf("MaxTurns = %d, want 500", cfg.MaxTurns)
		}
		if cfg.MaxFixIterations != 7 {
			t.Errorf("MaxFixIterations = %d, want 7", cfg.MaxFixIterations)
		}
	})
}
