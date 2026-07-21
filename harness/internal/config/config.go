package config

import (
	"os"
	"strconv"
)

const (
	DefaultMaxTurns         = 200
	DefaultMaxFixIterations = 3
)

type Config struct {
	Model            string
	Provider         string
	Endpoint         string
	APIKey           string
	MaxTurns         int
	MaxFixIterations int
}

func LoadFromEnv() *Config {
	model := os.Getenv("KONVEYOR_MODEL_PRIMARY_MODEL")
	provider := os.Getenv("KONVEYOR_MODEL_PRIMARY_PROVIDER")
	if model == "" || provider == "" {
		return nil
	}

	cfg := &Config{
		Model:            model,
		Provider:         provider,
		Endpoint:         os.Getenv("KONVEYOR_MODEL_PRIMARY_ENDPOINT"),
		APIKey:           os.Getenv("KONVEYOR_MODEL_PRIMARY_API_KEY"),
		MaxTurns:         DefaultMaxTurns,
		MaxFixIterations: DefaultMaxFixIterations,
	}

	if n, err := strconv.Atoi(os.Getenv("KONVEYOR_PARAM_MAX_TURNS")); err == nil && n > 0 {
		cfg.MaxTurns = n
	}
	if n, err := strconv.Atoi(os.Getenv("KONVEYOR_PARAM_MAX_FIX_ITERATIONS")); err == nil && n > 0 {
		cfg.MaxFixIterations = n
	}

	return cfg
}
