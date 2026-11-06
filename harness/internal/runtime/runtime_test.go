package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func envMap(env []string) map[string]string {
	values := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	return values
}

func TestLightspeedProviderMapping(t *testing.T) {
	for _, tc := range []struct{ provider, want, key string }{
		{"anthropic", "anthropic", "ANTHROPIC_API_KEY"},
		{"openai", "openai", "OPENAI_API_KEY"},
		{"aws-bedrock", "bedrock", ""},
		{"aws_bedrock", "bedrock", ""},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			env, err := lightspeedEnv([]string{"HUB_TOKEN=private", "APP_ID=42", "AWS_REGION=us-east-2", "LIGHTSPEED_TOOL_OUTPUT_INSPECTION_ENABLED=true", "LIGHTSPEED_PROVIDER=wrong"}, Config{Provider: tc.provider, Model: "selected", APIKey: "test-model-key", Endpoint: "https://model.example", SecretKey: "test-acp-key"}, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			values := envMap(env)
			if values["LIGHTSPEED_PROVIDER"] != tc.want || values["LIGHTSPEED_MODEL"] != "selected" || values["LIGHTSPEED_PROVIDER_URL"] != "https://model.example" {
				t.Fatalf("provider selection not mapped")
			}
			if tc.key != "" && values[tc.key] != "test-model-key" {
				t.Fatal("model credential not mapped")
			}
			if _, present := values["HUB_TOKEN"]; present {
				t.Fatal("Hub token forwarded")
			}
			if _, present := values["APP_ID"]; present {
				t.Fatal("Hub metadata forwarded")
			}
			if values["LIGHTSPEED_TOOL_OUTPUT_INSPECTION_ENABLED"] != "true" || values["AWS_REGION"] != "us-east-2" {
				t.Fatal("runtime inspection or AWS configuration lost")
			}
		})
	}
}

func TestLightspeedVertexCredentials(t *testing.T) {
	for _, tc := range []struct{ model, family string }{{"claude-sonnet-4-5", "anthropic"}, {"gemini-2.5-pro", "google"}} {
		t.Run(tc.family, func(t *testing.T) {
			dir := t.TempDir()
			content := `{"type":"service_account","project_id":"fixture"}`
			env, err := lightspeedEnv([]string{"GOOGLE_APPLICATION_CREDENTIALS_JSON=" + content, "GOOGLE_CLOUD_PROJECT=fixture", "GOOGLE_CLOUD_LOCATION=us-east5"}, Config{Provider: "gcp-vertex-ai", Model: tc.model}, dir)
			if err != nil {
				t.Fatal(err)
			}
			values := envMap(env)
			if values["LIGHTSPEED_PROVIDER"] != "vertex" || values["LIGHTSPEED_MODEL_PROVIDER"] != tc.family || values["LIGHTSPEED_LLM_CREDENTIALS_PATH"] != dir {
				t.Fatal("Vertex mapping incorrect")
			}
			if _, ok := values["GOOGLE_APPLICATION_CREDENTIALS_JSON"]; ok {
				t.Fatal("raw ADC JSON inherited")
			}
			path := filepath.Join(dir, "GOOGLE_APPLICATION_CREDENTIALS")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != content {
				t.Fatal("ADC changed")
			}
			st, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if st.Mode().Perm() != 0600 {
				t.Fatalf("ADC permissions=%o", st.Mode().Perm())
			}
		})
	}
}

func TestLightspeedRejectsUnsupportedProvider(t *testing.T) {
	_, err := lightspeedEnv(nil, Config{Provider: "google"}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error=%v", err)
	}
}

func TestLightspeedBudgetRejectedBeforeProcessStartup(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	srv, err := Start(context.Background(), Config{Name: "lightspeed", MaxCost: 1})
	if srv != nil || err == nil || !strings.Contains(err.Error(), "monetary usage is unavailable") {
		t.Fatalf("runtime=%v error=%v", srv, err)
	}
	if err := Validate("goose", 1, false); err != nil {
		t.Fatal("Goose cost budget changed", err)
	}
}

func TestLightspeedProcessContractAndCleanup(t *testing.T) {
	bin := t.TempDir()
	// No Python import or model call: inspect the actual launch contract of a
	// long-lived child and prove private config cleanup on shutdown.
	script := "#!/bin/sh\ntrap 'exit 0' TERM\nwhile :; do /bin/sleep 0.05; done\n"
	if err := os.WriteFile(filepath.Join(bin, "python3"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := Start(ctx, Config{Name: "lightspeed", Provider: "openai", Model: "fixture", Port: 4001, BindLoopback: true, SecretKey: "fixture-key", Cwd: "/workspace/repo", MaxTurns: 20, Mode: "approve", AskUser: &AskUserConfig{Command: "/usr/local/bin/migration-harness", Args: []string{"ask-user"}}})
	if err != nil {
		t.Fatal(err)
	}
	srv := p.(*lightspeedProcess)
	defer srv.Stop()
	args := srv.cmd.Args
	if len(args) != 9 || args[1] != "-m" || args[2] != "lightspeed_agentic.acp" || args[4] != "127.0.0.1" || args[6] != "4001" {
		t.Fatalf("args=%q", args)
	}
	var cfg lightspeedConfig
	data, err := os.ReadFile(args[len(args)-1])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "approve" || cfg.Cwd != "/workspace/repo" || cfg.MaxTurns != 17 || cfg.AskUser == nil {
		t.Fatalf("config=%+v", cfg)
	}
	if !srv.Alive() || srv.Port() != 4001 || srv.SecretKey() != "fixture-key" {
		t.Fatal("process state incorrect")
	}
	dir := srv.tempDir
	if err := srv.Stop(); err != nil {
		t.Fatal(err)
	}
	if srv.Alive() {
		t.Fatal("process remains alive")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("temporary config remains: %v", err)
	}
}

func TestLightspeedRequiresViewerTee(t *testing.T) {
	if err := Validate("lightspeed", 0, false); err == nil || !strings.Contains(err.Error(), "HARNESS_ACP_TEE=off") {
		t.Fatalf("error=%v", err)
	}
}
