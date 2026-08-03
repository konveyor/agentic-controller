package config

import (
	"fmt"
	"os"
	"strconv"
)

const (
	DefaultMaxTurns = 200
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

	// Prompt context layers, composed by internal/prompt.
	AgentPrompt       string
	WorkflowGuide     string
	StageInstructions string
}

func LoadFromEnv() (*Config, error) {
	required := map[string]string{
		"KONVEYOR_MODEL_PRIMARY_MODEL":    os.Getenv("KONVEYOR_MODEL_PRIMARY_MODEL"),
		"KONVEYOR_MODEL_PRIMARY_PROVIDER": os.Getenv("KONVEYOR_MODEL_PRIMARY_PROVIDER"),
		"HUB_BASE_URL":                    os.Getenv("HUB_BASE_URL"),
		"APP_ID":                          os.Getenv("APP_ID"),
		"KONVEYOR_ACP_SECRET_KEY":         os.Getenv("KONVEYOR_ACP_SECRET_KEY"),
		"TARGET_BRANCH":                   os.Getenv("TARGET_BRANCH"),
	}
	for k, v := range required {
		if v == "" {
			return nil, fmt.Errorf("required env var %s is not set", k)
		}
	}

	cfg := &Config{
		Model:        required["KONVEYOR_MODEL_PRIMARY_MODEL"],
		Provider:     required["KONVEYOR_MODEL_PRIMARY_PROVIDER"],
		Endpoint:     os.Getenv("KONVEYOR_MODEL_PRIMARY_ENDPOINT"),
		APIKey:       os.Getenv("KONVEYOR_MODEL_PRIMARY_API_KEY"),
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

	return cfg, nil
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
