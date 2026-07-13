package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	clearKonveyorEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config")

	original := &Config{
		Model:            "gemini-2.5-pro",
		Provider:         "gcp_vertex_ai",
		MaxTurns:         300,
		MaxFixIterations: 5,
	}

	if err := Save(path, original); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Model != original.Model {
		t.Errorf("Model = %q, want %q", loaded.Model, original.Model)
	}
	if loaded.Provider != original.Provider {
		t.Errorf("Provider = %q, want %q", loaded.Provider, original.Provider)
	}
	if loaded.MaxTurns != original.MaxTurns {
		t.Errorf("MaxTurns = %d, want %d", loaded.MaxTurns, original.MaxTurns)
	}
	if loaded.MaxFixIterations != original.MaxFixIterations {
		t.Errorf("MaxFixIterations = %d, want %d", loaded.MaxFixIterations, original.MaxFixIterations)
	}
}

func TestLoadDefaults(t *testing.T) {
	clearKonveyorEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config")

	content := "MH_MODEL=\"test-model\"\nMH_PROVIDER=\"test-provider\"\n"
	os.WriteFile(path, []byte(content), 0600)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.MaxTurns != DefaultMaxTurns {
		t.Errorf("MaxTurns = %d, want default %d", cfg.MaxTurns, DefaultMaxTurns)
	}
	if cfg.MaxFixIterations != DefaultMaxFixIterations {
		t.Errorf("MaxFixIterations = %d, want default %d", cfg.MaxFixIterations, DefaultMaxFixIterations)
	}
}

func TestLoadMissingModel(t *testing.T) {
	clearKonveyorEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config")

	content := "MH_PROVIDER=\"test-provider\"\n"
	os.WriteFile(path, []byte(content), 0600)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing MH_MODEL")
	}
}

func TestLoadMissingProvider(t *testing.T) {
	clearKonveyorEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config")

	content := "MH_MODEL=\"test-model\"\n"
	os.WriteFile(path, []byte(content), 0600)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing MH_PROVIDER")
	}
}

func TestLoadMissingFile(t *testing.T) {
	clearKonveyorEnv(t)
	_, err := Load("/nonexistent/path/config")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestSavePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")

	cfg := &Config{Model: "m", Provider: "p", MaxTurns: 100, MaxFixIterations: 2}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("permissions = %o, want 0600", perm)
	}
}

func clearKonveyorEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"KONVEYOR_MODEL_PRIMARY_MODEL",
		"KONVEYOR_MODEL_PRIMARY_PROVIDER",
		"KONVEYOR_MODEL_PRIMARY_ENDPOINT",
		"KONVEYOR_MODEL_PRIMARY_API_KEY",
		"KONVEYOR_PARAM_MAX_TURNS",
		"KONVEYOR_PARAM_MAX_FIX_ITERATIONS",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Run("returns config from env", func(t *testing.T) {
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

func TestLoadPrefersEnvOverFile(t *testing.T) {
	t.Setenv("KONVEYOR_MODEL_PRIMARY_MODEL", "env-model")
	t.Setenv("KONVEYOR_MODEL_PRIMARY_PROVIDER", "env-provider")

	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	content := "MH_MODEL=\"file-model\"\nMH_PROVIDER=\"file-provider\"\n"
	os.WriteFile(path, []byte(content), 0600)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Model != "env-model" {
		t.Errorf("Model = %q, want %q (env should take precedence)", cfg.Model, "env-model")
	}
	if cfg.Provider != "env-provider" {
		t.Errorf("Provider = %q, want %q (env should take precedence)", cfg.Provider, "env-provider")
	}
}

func TestParseConfigLineVariants(t *testing.T) {
	tests := []struct {
		line    string
		wantKey string
		wantVal string
		wantOK  bool
	}{
		{`MH_MODEL="claude"`, "MH_MODEL", "claude", true},
		{`MH_MODEL='claude'`, "MH_MODEL", "claude", true},
		{`MH_MODEL=claude`, "MH_MODEL", "claude", true},
		{`# comment`, "", "", false},
		{`no-equals-sign`, "", "", false},
	}

	for _, tt := range tests {
		key, val, ok := parseConfigLine(tt.line)
		if ok != tt.wantOK || key != tt.wantKey || val != tt.wantVal {
			t.Errorf("parseConfigLine(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.line, key, val, ok, tt.wantKey, tt.wantVal, tt.wantOK)
		}
	}
}
