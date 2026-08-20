package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultMaxTurns = 200

	// MaxHITLTimeoutSeconds caps HARNESS_HITL_TIMEOUT_SECONDS (10 min).
	MaxHITLTimeoutSeconds = 600
)

type Config struct {
	Model    string
	Provider string
	Endpoint string
	APIKey   string
	MaxTurns int

	HubBaseURL   string
	HubToken     string
	HubTokenID   string
	AppID        string
	ACPSecretKey string

	TargetBranch string

	// Workflow stage metadata, injected by the controller for
	// AgentWorkflowRun stages. Both empty for standalone AgentRuns.
	WorkflowStage      string
	WorkflowStageCount string

	// ACPTee: the harness fronts the pod ACP port and tees the run's
	// live stream to attached viewers (default on; HARNESS_ACP_TEE=off
	// restores goose owning the port directly).
	ACPTee bool
	// HITLTimeout: how long a permission ask waits for an attached
	// viewer before the headless fallback (HARNESS_HITL_TIMEOUT_SECONDS).
	HITLTimeout time.Duration
	// HITLSteer: attached viewers may redirect the run session —
	// `_goose/unstable/session/steer` and `session/cancel` frames naming
	// the run session are relayed onto the run connection (default on;
	// HARNESS_HITL_STEER=off makes the run stream watch-only).
	HITLSteer bool
	// HITLAsk: the agent gets an ask_user tool (the harness's own stdio
	// MCP server) whose questions reach attached viewers as
	// elicitation/create asks and block the turn until answered; with
	// nobody watching they fail closed (cancelled, never answered for the
	// human). Default on; HARNESS_HITL_ASK=off leaves the tool out.
	HITLAsk bool

	// Prompt context layers, composed by internal/prompt.
	AgentPrompt       string
	WorkflowGuide     string
	StageInstructions string
}

// envWithFallback reads primary first, falling back to fallback.
// This supports the transition from KONVEYOR_MODEL_PRIMARY_* to
// KONVEYOR_LLM_* env var names. Drop the fallback once all deployed
// controllers use the new names.
func envWithFallback(primary, fallback string) string {
	if v := os.Getenv(primary); v != "" {
		return v
	}
	return os.Getenv(fallback)
}

func LoadFromEnv() (*Config, error) {
	model := envWithFallback("KONVEYOR_LLM_MODEL", "KONVEYOR_MODEL_PRIMARY_MODEL")

	required := map[string]string{
		"KONVEYOR_LLM_MODEL":      model,
		"HUB_BASE_URL":            os.Getenv("HUB_BASE_URL"),
		"APP_ID":                  os.Getenv("APP_ID"),
		"KONVEYOR_ACP_SECRET_KEY": os.Getenv("KONVEYOR_ACP_SECRET_KEY"),
		"TARGET_BRANCH":           os.Getenv("TARGET_BRANCH"),
	}
	for k, v := range required {
		if v == "" {
			return nil, fmt.Errorf("required env var %s is not set", k)
		}
	}

	cfg := &Config{
		Model:        model,
		Provider:     envWithFallback("KONVEYOR_LLM_PROVIDER", "KONVEYOR_MODEL_PRIMARY_PROVIDER"),
		Endpoint:     envWithFallback("KONVEYOR_LLM_ENDPOINT", "KONVEYOR_MODEL_PRIMARY_ENDPOINT"),
		APIKey:       envWithFallback("KONVEYOR_LLM_API_KEY", "KONVEYOR_MODEL_PRIMARY_API_KEY"),
		MaxTurns:     DefaultMaxTurns,
		HubBaseURL:   required["HUB_BASE_URL"],
		HubToken:     os.Getenv("HUB_TOKEN"),
		HubTokenID:   os.Getenv("HUB_TOKEN_ID"),
		AppID:        required["APP_ID"],
		ACPSecretKey: required["KONVEYOR_ACP_SECRET_KEY"],
		TargetBranch: required["TARGET_BRANCH"],

		WorkflowStage:      os.Getenv("KONVEYOR_WORKFLOW_STAGE"),
		WorkflowStageCount: os.Getenv("KONVEYOR_WORKFLOW_STAGE_COUNT"),

		AgentPrompt:       os.Getenv("KONVEYOR_PROMPT"),
		WorkflowGuide:     workflowGuideFromEnv(),
		StageInstructions: os.Getenv("KONVEYOR_INSTRUCTIONS"),
	}

	if n, err := strconv.Atoi(os.Getenv("KONVEYOR_PARAM_MAX_TURNS")); err == nil && n > 0 {
		cfg.MaxTurns = n
	}

	// Default-ON kill switches: the one E2E path must exercise the tee
	// (and steering), so only an explicit opt-out disables them.
	cfg.ACPTee = !envSwitchedOff("HARNESS_ACP_TEE")
	cfg.HITLSteer = !envSwitchedOff("HARNESS_HITL_STEER")
	cfg.HITLAsk = !envSwitchedOff("HARNESS_HITL_ASK")
	if n, err := strconv.Atoi(os.Getenv("HARNESS_HITL_TIMEOUT_SECONDS")); err == nil && n > 0 {
		// Ceiling: a single ask parking the run for hours isn't HITL,
		// it's abandonment — the pod deadline should not be spent inside
		// one unanswered dialog.
		if n > MaxHITLTimeoutSeconds {
			n = MaxHITLTimeoutSeconds
		}
		cfg.HITLTimeout = time.Duration(n) * time.Second
	}

	return cfg, nil
}

// envSwitchedOff reports whether a default-ON feature env var is set to an
// explicit opt-out value.
func envSwitchedOff(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "off", "false", "0", "disabled":
		return true
	}
	return false
}

// workflowGuideFromEnv reads the workflow guide the controller injects.
//
// The canonical env var is KONVEYOR_WORKFLOW_GUIDE (set by the controller).
// KONVEYOR_PLAYBOOK_INSTRUCTIONS is the legacy name; drop the fallback
// once all deployed controllers use the new name.
func workflowGuideFromEnv() string {
	if v := os.Getenv("KONVEYOR_WORKFLOW_GUIDE"); v != "" {
		return v
	}
	return os.Getenv("KONVEYOR_PLAYBOOK_INSTRUCTIONS")
}
