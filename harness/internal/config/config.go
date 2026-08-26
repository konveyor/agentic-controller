package config

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/konveyor/migration-harness/internal/params"
)

const (
	DefaultMaxTurns = 200

	// MaxHITLTimeoutSeconds caps HARNESS_HITL_TIMEOUT_SECONDS (10 min).
	MaxHITLTimeoutSeconds = 600

	// Default git commit identity used when the Agent/AgentRun does not
	// configure one. Preserves the historical hardcoded author.
	DefaultGitAuthorName  = "migration-agent"
	DefaultGitAuthorEmail = "migration-agent@konveyor.io"
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

	// GitAuthorName / GitAuthorEmail are the git commit identity the
	// harness applies before the agent commits. Sourced from the
	// Agent/AgentRun gitConfig (via the controller) and defaulted to
	// DefaultGitAuthor* when unset.
	GitAuthorName  string
	GitAuthorEmail string

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

	// Params is the parsed /run/konveyor/params.json (ADR 0009): workflow
	// and agent parameter values, plus resolved execution controls. Its
	// MaxTurns, when present, overrides the default above.
	Params params.File

	// CostLimit is params.ReserveFraction of the parsed
	// execution.maxCost, in USD; 0 when maxCost is unset. SendPrompt
	// cancels the primary prompt when cumulative usage_update cost
	// reaches this threshold (ADR 0011).
	CostLimit float64
	// MaxCost is the unreserved execution.maxCost ceiling, in USD; 0
	// when unset. ACP cost is cumulative for the whole session, so the
	// handoff prompt is allowed to spend up to this full budget rather
	// than CostLimit's reserved fraction — otherwise cumulative spend
	// already sits at ~85% of MaxCost when the primary prompt stops,
	// leaving the handoff no room to run (ADR 0011).
	MaxCost float64
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

// gitAuthorFromEnv reads the commit identity as one indivisible pair: both
// KONVEYOR_GIT_AUTHOR_NAME and KONVEYOR_GIT_AUTHOR_EMAIL must be present to
// take effect, otherwise the historical default identity is used for both.
// Defaulting the two independently would let a half-set environment (e.g. a
// single user-injected variable) mix a supplied name with the default email,
// producing a hybrid author the CRD validation never sanctioned.
func gitAuthorFromEnv() (name, email string) {
	name = os.Getenv("KONVEYOR_GIT_AUTHOR_NAME")
	email = os.Getenv("KONVEYOR_GIT_AUTHOR_EMAIL")
	if name == "" || email == "" {
		return DefaultGitAuthorName, DefaultGitAuthorEmail
	}
	return name, email
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

	gitAuthorName, gitAuthorEmail := gitAuthorFromEnv()

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

		GitAuthorName:  gitAuthorName,
		GitAuthorEmail: gitAuthorEmail,

		WorkflowStage:      os.Getenv("KONVEYOR_WORKFLOW_STAGE"),
		WorkflowStageCount: os.Getenv("KONVEYOR_WORKFLOW_STAGE_COUNT"),

		AgentPrompt:       os.Getenv("KONVEYOR_PROMPT"),
		WorkflowGuide:     workflowGuideFromEnv(),
		StageInstructions: os.Getenv("KONVEYOR_INSTRUCTIONS"),
	}

	paramsFile, err := params.Load(paramsFilePath())
	if err != nil {
		return nil, fmt.Errorf("load params: %w", err)
	}
	cfg.Params = paramsFile
	if n, ok := paramsFile.MaxTurns(); ok {
		cfg.MaxTurns = n
	}

	if paramsFile.Execution.MaxCost != "" {
		parsed, err := strconv.ParseFloat(paramsFile.Execution.MaxCost, 64)
		if err != nil {
			return nil, fmt.Errorf("execution.maxCost %q is not numeric: %w", paramsFile.Execution.MaxCost, err)
		}
		if math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < 0 {
			return nil, fmt.Errorf("execution.maxCost %q must be a finite non-negative number", paramsFile.Execution.MaxCost)
		}
		if parsed > 0 {
			cfg.MaxCost = parsed
			cfg.CostLimit = parsed * params.ReserveFraction
		}
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

// paramsFilePath returns the path to the controller-written params.json
// (ADR 0009). HARNESS_PARAMS_FILE overrides the contract path, for tests
// and for running the harness outside a Sandbox pod (see hack/harness-*).
func paramsFilePath() string {
	if v := os.Getenv("HARNESS_PARAMS_FILE"); v != "" {
		return v
	}
	return params.Path
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
