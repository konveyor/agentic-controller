package config

import (
	"os"
	"strings"
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
		"HUB_BASE_URL",
		"HUB_TOKEN",
		"APP_ID",
		"KONVEYOR_ACP_SECRET_KEY",
		"TARGET_BRANCH",
		"KONVEYOR_PROMPT",
		"KONVEYOR_PLAYBOOK_INSTRUCTIONS",
		"KONVEYOR_WORKFLOW_GUIDE",
		"KONVEYOR_INSTRUCTIONS",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("KONVEYOR_MODEL_PRIMARY_MODEL", "claude-sonnet-4-5")
	t.Setenv("KONVEYOR_MODEL_PRIMARY_PROVIDER", "anthropic")
	t.Setenv("HUB_BASE_URL", "https://hub.example.com")
	t.Setenv("APP_ID", "42")
	t.Setenv("KONVEYOR_ACP_SECRET_KEY", "test-secret-key")
	t.Setenv("TARGET_BRANCH", "migration-1234")
}

func TestLoadFromEnv(t *testing.T) {
	t.Run("returns config from env", func(t *testing.T) {
		clearKonveyorEnv(t)
		setRequiredEnv(t)
		t.Setenv("KONVEYOR_MODEL_PRIMARY_ENDPOINT", "https://api.anthropic.com")
		t.Setenv("KONVEYOR_MODEL_PRIMARY_API_KEY", "sk-test-key")

		cfg, err := LoadFromEnv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
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
		if cfg.ACPSecretKey != "test-secret-key" {
			t.Errorf("ACPSecretKey = %q, want %q", cfg.ACPSecretKey, "test-secret-key")
		}
		if cfg.MaxTurns != DefaultMaxTurns {
			t.Errorf("MaxTurns = %d, want default %d", cfg.MaxTurns, DefaultMaxTurns)
		}
	})

	t.Run("errors when env not set", func(t *testing.T) {
		clearKonveyorEnv(t)

		_, err := LoadFromEnv()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("error names the missing var", func(t *testing.T) {
		clearKonveyorEnv(t)
		setRequiredEnv(t)
		os.Unsetenv("KONVEYOR_ACP_SECRET_KEY")

		_, err := LoadFromEnv()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "KONVEYOR_ACP_SECRET_KEY") {
			t.Errorf("error should name missing var, got: %v", err)
		}
	})

	t.Run("errors when TARGET_BRANCH is empty", func(t *testing.T) {
		clearKonveyorEnv(t)
		setRequiredEnv(t)
		os.Unsetenv("TARGET_BRANCH")

		_, err := LoadFromEnv()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "TARGET_BRANCH") {
			t.Errorf("error should name TARGET_BRANCH, got: %v", err)
		}
	})

	t.Run("populates TargetBranch", func(t *testing.T) {
		clearKonveyorEnv(t)
		setRequiredEnv(t)
		t.Setenv("TARGET_BRANCH", "konveyor/migrate-42")

		cfg, err := LoadFromEnv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.TargetBranch != "konveyor/migrate-42" {
			t.Errorf("TargetBranch = %q, want %q", cfg.TargetBranch, "konveyor/migrate-42")
		}
	})

	t.Run("reads optional param overrides", func(t *testing.T) {
		clearKonveyorEnv(t)
		setRequiredEnv(t)
		t.Setenv("KONVEYOR_PARAM_MAX_TURNS", "500")

		cfg, err := LoadFromEnv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.MaxTurns != 500 {
			t.Errorf("MaxTurns = %d, want 500", cfg.MaxTurns)
		}
	})
}

func TestLoadFromEnvReadsPromptLayers(t *testing.T) {
	clearKonveyorEnv(t)
	setRequiredEnv(t)
	t.Setenv("KONVEYOR_PROMPT", "AGENT PROMPT")
	t.Setenv("KONVEYOR_WORKFLOW_GUIDE", "WORKFLOW GUIDE")
	t.Setenv("KONVEYOR_INSTRUCTIONS", "STAGE TASK")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.AgentPrompt != "AGENT PROMPT" {
		t.Errorf("AgentPrompt = %q", cfg.AgentPrompt)
	}
	if cfg.WorkflowGuide != "WORKFLOW GUIDE" {
		t.Errorf("WorkflowGuide = %q", cfg.WorkflowGuide)
	}
	if cfg.StageInstructions != "STAGE TASK" {
		t.Errorf("StageInstructions = %q", cfg.StageInstructions)
	}
}

// #80 renames the env var; the harness reads either so merge order does not
// matter. Remove with the fallback once #80 has landed everywhere.
func TestLoadFromEnvFallsBackToPlaybookInstructions(t *testing.T) {
	clearKonveyorEnv(t)
	setRequiredEnv(t)
	t.Setenv("KONVEYOR_PLAYBOOK_INSTRUCTIONS", "OLD NAME")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.WorkflowGuide != "OLD NAME" {
		t.Errorf("WorkflowGuide = %q, want the KONVEYOR_PLAYBOOK_INSTRUCTIONS value", cfg.WorkflowGuide)
	}
}

func TestLoadFromEnvPrefersWorkflowGuide(t *testing.T) {
	clearKonveyorEnv(t)
	setRequiredEnv(t)
	t.Setenv("KONVEYOR_PLAYBOOK_INSTRUCTIONS", "OLD NAME")
	t.Setenv("KONVEYOR_WORKFLOW_GUIDE", "NEW NAME")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.WorkflowGuide != "NEW NAME" {
		t.Errorf("WorkflowGuide = %q, want the KONVEYOR_WORKFLOW_GUIDE value", cfg.WorkflowGuide)
	}
}
