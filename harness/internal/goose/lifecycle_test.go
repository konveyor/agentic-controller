package goose

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProviderEnvAnthropic(t *testing.T) {
	env, _ := providerEnv(nil, "anthropic", "claude-sonnet-4-5", "sk-ant-key", "https://api.anthropic.com")
	assertEnvContains(t, env, "GOOSE_PROVIDER", "anthropic")
	assertEnvContains(t, env, "GOOSE_MODEL", "claude-sonnet-4-5")
	assertEnvContains(t, env, "ANTHROPIC_API_KEY", "sk-ant-key")
	assertEnvContains(t, env, "ANTHROPIC_HOST", "https://api.anthropic.com")
}

func TestProviderEnvOpenAI(t *testing.T) {
	env, _ := providerEnv(nil, "openai", "gpt-4o", "sk-openai-key", "https://api.openai.com")
	assertEnvContains(t, env, "GOOSE_PROVIDER", "openai")
	assertEnvContains(t, env, "OPENAI_API_KEY", "sk-openai-key")
	assertEnvContains(t, env, "OPENAI_HOST", "https://api.openai.com")
}

func TestProviderEnvGoogle(t *testing.T) {
	env, _ := providerEnv(nil, "google", "gemini-2.5-pro", "google-key", "")
	assertEnvContains(t, env, "GOOGLE_API_KEY", "google-key")
	assertEnvNotPresent(t, env, "ANTHROPIC_API_KEY")
}

func TestProviderEnvAWSBedrock(t *testing.T) {
	env, _ := providerEnv(nil, "aws-bedrock", "us.anthropic.claude-sonnet-4-5-20250929-v1:0", "unused-key", "https://bedrock-runtime.us-east-1.amazonaws.com")
	assertEnvContains(t, env, "GOOSE_PROVIDER", "aws_bedrock")
	assertEnvContains(t, env, "GOOSE_MODEL", "us.anthropic.claude-sonnet-4-5-20250929-v1:0")
	assertEnvNotPresent(t, env, "ANTHROPIC_API_KEY")
	assertEnvNotPresent(t, env, "OPENAI_API_KEY")
	assertEnvNotPresent(t, env, "ANTHROPIC_HOST")
	assertEnvNotPresent(t, env, "OPENAI_HOST")
}

// The ambient environment must not decide what the assertions above
// conclude. providerEnv appends to the base it is handed, so a test
// asking what the harness added hands it nil and gets an answer about
// the harness. While the base was an os.Environ() call inside the
// function, a developer with OPENAI_API_KEY exported failed
// TestProviderEnvAWSBedrock whatever the harness did — and the failure
// printed their key.
func TestProviderEnvAsksAboutTheHarnessNotTheShell(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "ambient-not-from-the-harness")
	t.Setenv("ANTHROPIC_API_KEY", "ambient-not-from-the-harness")

	env, _ := providerEnv(nil, "aws-bedrock", "us.anthropic.claude-sonnet-4-5-20250929-v1:0", "unused-key", "")
	assertEnvNotPresent(t, env, "OPENAI_API_KEY")
	assertEnvNotPresent(t, env, "ANTHROPIC_API_KEY")

	// What the caller does hand over is carried through, and left alone:
	// in production the base is os.Environ(), which goose needs.
	base := make([]string, 1, 8)
	base[0] = "PATH=/usr/bin"
	withBase, _ := providerEnv(base, "aws-bedrock", "", "", "")
	assertEnvContains(t, withBase, "PATH", "/usr/bin")
	if len(base) != 1 || base[0] != "PATH=/usr/bin" {
		t.Errorf("providerEnv wrote through to the base slice it was handed: %v", base)
	}
}

func TestProviderEnvNormalizesHyphens(t *testing.T) {
	env, _ := providerEnv(nil, "gcp-vertex-ai", "", "", "")
	assertEnvContains(t, env, "GOOSE_PROVIDER", "gcp_vertex_ai")
}

func TestProviderEnvEmptyStringsAddNothing(t *testing.T) {
	env, _ := providerEnv(nil, "", "", "", "")
	assertEnvNotPresent(t, env, "GOOSE_PROVIDER")
	assertEnvNotPresent(t, env, "GOOSE_MODEL")
	assertEnvNotPresent(t, env, "ANTHROPIC_API_KEY")
}

func TestProviderEnvGCPVertexFiltersCredJSON(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS_JSON", `{"type":"service_account"}`)
	env, tempDirs := providerEnv(os.Environ(), "gcp-vertex-ai", "", "", "")
	for _, d := range tempDirs {
		defer os.RemoveAll(d)
	}
	assertEnvNotPresent(t, env, "GOOGLE_APPLICATION_CREDENTIALS_JSON")
	assertEnvPresent(t, env, "GOOGLE_APPLICATION_CREDENTIALS")
}

func TestFilterEnvKey(t *testing.T) {
	env := []string{"FOO=bar", "SECRET=hunter2", "FOO_BAR=baz"}
	filtered := filterEnvKey(env, "SECRET")
	if len(filtered) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(filtered))
	}
	assertEnvNotPresent(t, filtered, "SECRET")
	assertEnvPresent(t, filtered, "FOO")
	assertEnvPresent(t, filtered, "FOO_BAR")
}

func TestFilterEnvKeyNoMatch(t *testing.T) {
	env := []string{"A=1", "B=2"}
	filtered := filterEnvKey(env, "C")
	if len(filtered) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(filtered))
	}
}

func TestWriteADCFile(t *testing.T) {
	content := `{"type":"service_account","project_id":"test"}`
	path, err := writeADCFile(content)
	if err != nil {
		t.Fatalf("writeADCFile: %v", err)
	}
	defer os.RemoveAll(filepath.Dir(path))

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != content {
		t.Errorf("content = %q, want %q", string(data), content)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("permissions = %o, want 0600", info.Mode().Perm())
	}
}

func assertEnvContains(t *testing.T, env []string, key, value string) {
	t.Helper()
	want := key + "=" + value
	for _, e := range env {
		if e == want {
			return
		}
	}
	t.Errorf("env missing %s=%s", key, value)
}

func assertEnvPresent(t *testing.T, env []string, key string) {
	t.Helper()
	prefix := key + "="
	for _, e := range env {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			return
		}
	}
	t.Errorf("env missing key %s", key)
}

func assertEnvNotPresent(t *testing.T, env []string, key string) {
	t.Helper()
	prefix := key + "="
	for _, e := range env {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			// The key only, never the entry: the value may be a real
			// credential, and a failure message is printed to the test
			// log and to CI output.
			t.Errorf("env should not contain key %s", key)
		}
	}
}

func TestExecutionEnvSetsModeAndDefaultsToAuto(t *testing.T) {
	env := executionEnv("approve", 0)
	assertEnvContains(t, env, "GOOSE_MODE", "approve")

	env = executionEnv("", 0)
	assertEnvContains(t, env, "GOOSE_MODE", "auto")
}

func TestExecutionEnvOmitsMaxTurnsWhenUnset(t *testing.T) {
	env := executionEnv("auto", 0)
	assertEnvNotPresent(t, env, "GOOSE_MAX_TURNS")
}

func TestExecutionEnvAppliesReserveFraction(t *testing.T) {
	env := executionEnv("auto", 200)
	assertEnvContains(t, env, "GOOSE_MAX_TURNS", "170")
}

func TestExecutionEnvClampsSmallBudgetsToAtLeastOne(t *testing.T) {
	env := executionEnv("auto", 1)
	assertEnvContains(t, env, "GOOSE_MAX_TURNS", "1")
}
