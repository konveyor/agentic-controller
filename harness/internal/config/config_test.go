package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func clearKonveyorEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		// New canonical names.
		"KONVEYOR_LLM_MODEL",
		"KONVEYOR_LLM_PROVIDER",
		"KONVEYOR_LLM_ENDPOINT",
		"KONVEYOR_LLM_API_KEY",
		// Legacy names (fallback).
		"KONVEYOR_MODEL_PRIMARY_MODEL",
		"KONVEYOR_MODEL_PRIMARY_PROVIDER",
		"KONVEYOR_MODEL_PRIMARY_ENDPOINT",
		"KONVEYOR_MODEL_PRIMARY_API_KEY",
		"HARNESS_PARAMS_FILE",
		"HUB_BASE_URL",
		"HUB_TOKEN",
		"APP_ID",
		"KONVEYOR_ACP_SECRET_KEY",
		"TARGET_BRANCH",
		"KONVEYOR_PROMPT",
		"KONVEYOR_PLAYBOOK_INSTRUCTIONS",
		"KONVEYOR_WORKFLOW_GUIDE",
		"KONVEYOR_INSTRUCTIONS",
		"KONVEYOR_WORKFLOW_STAGE",
		"KONVEYOR_WORKFLOW_STAGE_COUNT",
		"HUB_TOKEN_ID",
		"HARNESS_ACP_TEE",
		"HARNESS_HITL_STEER",
		"HARNESS_HITL_TIMEOUT_SECONDS",
		"KONVEYOR_GIT_AUTHOR_NAME",
		"KONVEYOR_GIT_AUTHOR_EMAIL",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("KONVEYOR_LLM_MODEL", "claude-sonnet-4-5")
	t.Setenv("HUB_BASE_URL", "https://hub.example.com")
	t.Setenv("APP_ID", "42")
	t.Setenv("KONVEYOR_ACP_SECRET_KEY", "test-secret-key")
	t.Setenv("TARGET_BRANCH", "migration-1234")
}

func TestLoadFromEnv(t *testing.T) {
	t.Run("returns config from env", func(t *testing.T) {
		clearKonveyorEnv(t)
		setRequiredEnv(t)
		t.Setenv("KONVEYOR_LLM_PROVIDER", "anthropic")
		t.Setenv("KONVEYOR_LLM_ENDPOINT", "https://api.anthropic.com")
		t.Setenv("KONVEYOR_LLM_API_KEY", "sk-test-key")

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

	t.Run("overrides max turns from params.json execution section", func(t *testing.T) {
		clearKonveyorEnv(t)
		setRequiredEnv(t)
		t.Setenv("HARNESS_PARAMS_FILE", writeParamsFile(t, `{"execution": {"maxTurns": 500}}`))

		cfg, err := LoadFromEnv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.MaxTurns != 500 {
			t.Errorf("MaxTurns = %d, want 500", cfg.MaxTurns)
		}
	})

	t.Run("defaults git author when unset", func(t *testing.T) {
		clearKonveyorEnv(t)
		setRequiredEnv(t)

		cfg, err := LoadFromEnv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.GitAuthorName != DefaultGitAuthorName {
			t.Errorf("GitAuthorName = %q, want %q", cfg.GitAuthorName, DefaultGitAuthorName)
		}
		if cfg.GitAuthorEmail != DefaultGitAuthorEmail {
			t.Errorf("GitAuthorEmail = %q, want %q", cfg.GitAuthorEmail, DefaultGitAuthorEmail)
		}
	})

	t.Run("reads git author from env", func(t *testing.T) {
		clearKonveyorEnv(t)
		setRequiredEnv(t)
		t.Setenv("KONVEYOR_GIT_AUTHOR_NAME", "Jane Dev")
		t.Setenv("KONVEYOR_GIT_AUTHOR_EMAIL", "jane@example.com")

		cfg, err := LoadFromEnv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.GitAuthorName != "Jane Dev" {
			t.Errorf("GitAuthorName = %q, want %q", cfg.GitAuthorName, "Jane Dev")
		}
		if cfg.GitAuthorEmail != "jane@example.com" {
			t.Errorf("GitAuthorEmail = %q, want %q", cfg.GitAuthorEmail, "jane@example.com")
		}
	})

	t.Run("falls back to the default pair when only one git author var is set", func(t *testing.T) {
		// A half-set identity must not mix a supplied name with the default
		// email; the pair defaults atomically.
		for _, tc := range []struct {
			name     string
			envName  string
			envValue string
		}{
			{name: "name only", envName: "KONVEYOR_GIT_AUTHOR_NAME", envValue: "Jane Dev"},
			{name: "email only", envName: "KONVEYOR_GIT_AUTHOR_EMAIL", envValue: "jane@example.com"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				clearKonveyorEnv(t)
				setRequiredEnv(t)
				t.Setenv(tc.envName, tc.envValue)

				cfg, err := LoadFromEnv()
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if cfg.GitAuthorName != DefaultGitAuthorName {
					t.Errorf("GitAuthorName = %q, want %q", cfg.GitAuthorName, DefaultGitAuthorName)
				}
				if cfg.GitAuthorEmail != DefaultGitAuthorEmail {
					t.Errorf("GitAuthorEmail = %q, want %q", cfg.GitAuthorEmail, DefaultGitAuthorEmail)
				}
			})
		}
	})

	t.Run("no params.json leaves the default max turns", func(t *testing.T) {
		clearKonveyorEnv(t)
		setRequiredEnv(t)
		t.Setenv("HARNESS_PARAMS_FILE", filepath.Join(t.TempDir(), "does-not-exist.json"))

		cfg, err := LoadFromEnv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.MaxTurns != DefaultMaxTurns {
			t.Errorf("MaxTurns = %d, want default %d", cfg.MaxTurns, DefaultMaxTurns)
		}
	})

	t.Run("errors on malformed params.json", func(t *testing.T) {
		clearKonveyorEnv(t)
		setRequiredEnv(t)
		t.Setenv("HARNESS_PARAMS_FILE", writeParamsFile(t, "{not json"))

		if _, err := LoadFromEnv(); err == nil {
			t.Fatal("expected error for malformed params.json, got nil")
		}
	})

	t.Run("populates Params from params.json for prompt rendering", func(t *testing.T) {
		clearKonveyorEnv(t)
		setRequiredEnv(t)
		t.Setenv("HARNESS_PARAMS_FILE", writeParamsFile(t, `{
			"workflow": {"application_name": "coolstore"},
			"agent": {"source_url": "https://github.com/example/app"}
		}`))

		cfg, err := LoadFromEnv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Params.Workflow["application_name"] != "coolstore" {
			t.Errorf("Params.Workflow[application_name] = %v", cfg.Params.Workflow["application_name"])
		}
		if cfg.Params.Agent["source_url"] != "https://github.com/example/app" {
			t.Errorf("Params.Agent[source_url] = %v", cfg.Params.Agent["source_url"])
		}
	})

	t.Run("computes CostLimit from execution.maxCost with the reserve fraction applied", func(t *testing.T) {
		clearKonveyorEnv(t)
		setRequiredEnv(t)
		t.Setenv("HARNESS_PARAMS_FILE", writeParamsFile(t, `{"execution": {"maxCost": "10.00"}}`))

		cfg, err := LoadFromEnv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.CostLimit != 8.5 {
			t.Errorf("CostLimit = %v, want 8.5 (10.00 * 0.85)", cfg.CostLimit)
		}
		if cfg.MaxCost != 10.00 {
			t.Errorf("MaxCost = %v, want 10.00 (unreserved)", cfg.MaxCost)
		}
	})

	t.Run("no maxCost leaves CostLimit and MaxCost at zero", func(t *testing.T) {
		clearKonveyorEnv(t)
		setRequiredEnv(t)

		cfg, err := LoadFromEnv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.CostLimit != 0 {
			t.Errorf("CostLimit = %v, want 0", cfg.CostLimit)
		}
		if cfg.MaxCost != 0 {
			t.Errorf("MaxCost = %v, want 0", cfg.MaxCost)
		}
	})

	t.Run("errors on non-numeric maxCost", func(t *testing.T) {
		clearKonveyorEnv(t)
		setRequiredEnv(t)
		t.Setenv("HARNESS_PARAMS_FILE", writeParamsFile(t, `{"execution": {"maxCost": "not-a-number"}}`))

		if _, err := LoadFromEnv(); err == nil {
			t.Fatal("expected error for non-numeric maxCost, got nil")
		}
	})

	for _, val := range []string{"0", "0.00"} {
		t.Run("explicit zero maxCost "+val+" leaves CostLimit and MaxCost at zero", func(t *testing.T) {
			clearKonveyorEnv(t)
			setRequiredEnv(t)
			t.Setenv("HARNESS_PARAMS_FILE", writeParamsFile(t, `{"execution": {"maxCost": "`+val+`"}}`))

			cfg, err := LoadFromEnv()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.CostLimit != 0 {
				t.Errorf("CostLimit = %v, want 0", cfg.CostLimit)
			}
			if cfg.MaxCost != 0 {
				t.Errorf("MaxCost = %v, want 0", cfg.MaxCost)
			}
		})
	}

	for _, tc := range []struct {
		name  string
		value string
	}{
		{"NaN", "NaN"},
		{"positive infinity", "+Inf"},
		{"negative infinity", "-Inf"},
		{"negative", "-1.5"},
	} {
		tc := tc
		t.Run("errors on invalid maxCost: "+tc.name, func(t *testing.T) {
			clearKonveyorEnv(t)
			setRequiredEnv(t)
			t.Setenv("HARNESS_PARAMS_FILE", writeParamsFile(t, `{"execution": {"maxCost": "`+tc.value+`"}}`))

			if _, err := LoadFromEnv(); err == nil {
				t.Fatalf("expected error for maxCost=%q, got nil", tc.value)
			}
		})
	}
}

// writeParamsFile writes content to a temp params.json and returns its path.
func writeParamsFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "params.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write params file: %v", err)
	}
	return path
}

// The legacy KONVEYOR_MODEL_PRIMARY_* env vars are still read as
// fallbacks. Remove this test when the fallback is dropped.
func TestLoadFromEnvFallsBackToLegacyModelVars(t *testing.T) {
	clearKonveyorEnv(t)

	// Set only legacy names — new names are unset.
	t.Setenv("KONVEYOR_MODEL_PRIMARY_MODEL", "gpt-4o")
	t.Setenv("KONVEYOR_MODEL_PRIMARY_PROVIDER", "openai")
	t.Setenv("KONVEYOR_MODEL_PRIMARY_ENDPOINT", "https://api.openai.com")
	t.Setenv("KONVEYOR_MODEL_PRIMARY_API_KEY", "sk-legacy")
	t.Setenv("HUB_BASE_URL", "https://hub.example.com")
	t.Setenv("APP_ID", "42")
	t.Setenv("KONVEYOR_ACP_SECRET_KEY", "test-secret-key")
	t.Setenv("TARGET_BRANCH", "migration-1234")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Model != "gpt-4o" {
		t.Errorf("Model = %q, want %q (legacy fallback)", cfg.Model, "gpt-4o")
	}
	if cfg.Provider != "openai" {
		t.Errorf("Provider = %q, want %q (legacy fallback)", cfg.Provider, "openai")
	}
	if cfg.Endpoint != "https://api.openai.com" {
		t.Errorf("Endpoint = %q, want %q (legacy fallback)", cfg.Endpoint, "https://api.openai.com")
	}
	if cfg.APIKey != "sk-legacy" {
		t.Errorf("APIKey = %q, want %q (legacy fallback)", cfg.APIKey, "sk-legacy")
	}
}

// When both new and legacy env vars are set, the new names take precedence.
func TestLoadFromEnvNewVarsTakePrecedence(t *testing.T) {
	clearKonveyorEnv(t)
	setRequiredEnv(t)

	// Set legacy values.
	t.Setenv("KONVEYOR_MODEL_PRIMARY_MODEL", "old-model")
	t.Setenv("KONVEYOR_MODEL_PRIMARY_PROVIDER", "old-provider")
	t.Setenv("KONVEYOR_MODEL_PRIMARY_ENDPOINT", "https://old.endpoint")
	t.Setenv("KONVEYOR_MODEL_PRIMARY_API_KEY", "sk-old")

	// Set new values (KONVEYOR_LLM_MODEL already set by setRequiredEnv).
	t.Setenv("KONVEYOR_LLM_PROVIDER", "anthropic")
	t.Setenv("KONVEYOR_LLM_ENDPOINT", "https://new.endpoint")
	t.Setenv("KONVEYOR_LLM_API_KEY", "sk-new")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Model != "claude-sonnet-4-5" {
		t.Errorf("Model = %q, want new value", cfg.Model)
	}
	if cfg.Provider != "anthropic" {
		t.Errorf("Provider = %q, want new value", cfg.Provider)
	}
	if cfg.Endpoint != "https://new.endpoint" {
		t.Errorf("Endpoint = %q, want new value", cfg.Endpoint)
	}
	if cfg.APIKey != "sk-new" {
		t.Errorf("APIKey = %q, want new value", cfg.APIKey)
	}
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

// The legacy env var KONVEYOR_PLAYBOOK_INSTRUCTIONS is still read as a
// fallback. Remove this test when the fallback is dropped.
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

func TestLoadFromEnvReadsWorkflowStageMetadata(t *testing.T) {
	clearKonveyorEnv(t)
	setRequiredEnv(t)
	t.Setenv("KONVEYOR_WORKFLOW_STAGE", "2")
	t.Setenv("KONVEYOR_WORKFLOW_STAGE_COUNT", "3")
	t.Setenv("HUB_TOKEN_ID", "99")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.WorkflowStage != "2" {
		t.Errorf("WorkflowStage = %q, want %q", cfg.WorkflowStage, "2")
	}
	if cfg.WorkflowStageCount != "3" {
		t.Errorf("WorkflowStageCount = %q, want %q", cfg.WorkflowStageCount, "3")
	}
	if cfg.HubTokenID != "99" {
		t.Errorf("HubTokenID = %q, want %q", cfg.HubTokenID, "99")
	}
}

func TestLoadFromEnvWorkflowStageFieldsOptional(t *testing.T) {
	clearKonveyorEnv(t)
	setRequiredEnv(t)

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.WorkflowStage != "" {
		t.Errorf("WorkflowStage should be empty for standalone runs, got %q", cfg.WorkflowStage)
	}
	if cfg.WorkflowStageCount != "" {
		t.Errorf("WorkflowStageCount should be empty for standalone runs, got %q", cfg.WorkflowStageCount)
	}
	if cfg.HubTokenID != "" {
		t.Errorf("HubTokenID should be empty when not set, got %q", cfg.HubTokenID)
	}
}

func TestHITLTimeoutClampedAndSteerSwitch(t *testing.T) {
	clearKonveyorEnv(t)
	setRequiredEnv(t)

	t.Setenv("HARNESS_HITL_TIMEOUT_SECONDS", "999999")
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if want := time.Duration(MaxHITLTimeoutSeconds) * time.Second; cfg.HITLTimeout != want {
		t.Errorf("HITLTimeout not clamped: got %v want %v", cfg.HITLTimeout, want)
	}
	if !cfg.HITLSteer {
		t.Error("HITLSteer should default on")
	}

	t.Setenv("HARNESS_HITL_STEER", " OFF ")
	cfg, err = LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.HITLSteer {
		t.Error("HARNESS_HITL_STEER=OFF (padded, uppercase) should disable steering")
	}
}
