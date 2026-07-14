package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

func DefaultHome() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".migration-harness")
}

func DefaultConfigPath() string {
	return filepath.Join(DefaultHome(), "config")
}

// LoadFromEnv builds a Config from the KONVEYOR_MODEL_* env vars injected
// by the agentic controller. Returns nil if the env vars are not set.
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

func Load(path string) (*Config, error) {
	if cfg := LoadFromEnv(); cfg != nil {
		return cfg, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	cfg := &Config{
		MaxTurns:         DefaultMaxTurns,
		MaxFixIterations: DefaultMaxFixIterations,
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := parseConfigLine(line)
		if !ok {
			continue
		}

		switch key {
		case "MH_MODEL":
			cfg.Model = value
		case "MH_PROVIDER":
			cfg.Provider = value
		case "MH_MAX_TURNS":
			if n, err := strconv.Atoi(value); err == nil {
				cfg.MaxTurns = n
			}
		case "MH_MAX_FIX_ITERATIONS":
			if n, err := strconv.Atoi(value); err == nil {
				cfg.MaxFixIterations = n
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	if cfg.Model == "" {
		return nil, fmt.Errorf("MH_MODEL is required in %s", path)
	}
	if cfg.Provider == "" {
		return nil, fmt.Errorf("MH_PROVIDER is required in %s", path)
	}

	return cfg, nil
}

func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	content := fmt.Sprintf(
		"MH_MODEL=\"%s\"\nMH_PROVIDER=\"%s\"\nMH_MAX_TURNS=\"%d\"\nMH_MAX_FIX_ITERATIONS=\"%d\"\n",
		cfg.Model, cfg.Provider, cfg.MaxTurns, cfg.MaxFixIterations,
	)

	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func parseConfigLine(line string) (key, value string, ok bool) {
	key, value, ok = strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return key, value, true
}
