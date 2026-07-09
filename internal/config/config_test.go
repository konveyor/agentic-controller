package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
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
