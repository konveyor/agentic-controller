package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konveyor/migration-harness/internal/proxy"
)

func TestProxyUsernameOnlyCredentialRedaction(t *testing.T) {
	for _, env := range []bool{false, true} {
		t.Run(map[bool]string{false: "Hub identity", true: "environment"}[env], func(t *testing.T) {
			const token = "token&username"
			var configs []proxy.Config
			if !env {
				configs = []proxy.Config{{Kind: "https", Host: "proxy", Username: token}}
			}
			policy, err := proxy.New(configs, func(key string) (string, bool) {
				if env && key == "HTTPS_PROXY" {
					return "http://token%26username@proxy:3128", true
				}
				return "", false
			})
			if err != nil {
				t.Fatal(err)
			}
			red := newRedactor()
			red.addSecrets(policy.Secrets()...)
			for _, value := range []string{token, "token%26username", "token&amp;username"} {
				if strings.Contains(red.redact("credential="+value), value) {
					t.Error("proxy username credential was not redacted")
				}
			}
		})
	}
}

func TestRunStagePreservesShortPlaceholderKeys(t *testing.T) {
	home := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/applications/42" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 42, "name": "placeholder-app", "repository": map[string]string{"url": filepath.Join(home, "none.git"), "branch": "main"}})
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	for key, value := range map[string]string{
		"HOME": home, "USERPROFILE": home, "KONVEYOR_LLM_MODEL": "test-model", "KONVEYOR_LLM_API_KEY": "none", "KONVEYOR_ACP_SECRET_KEY": "test-acp-key",
		"HUB_BASE_URL": server.URL, "HUB_TOKEN": "test-token", "HUB_TOKEN_ID": "", "APP_ID": "42", "TARGET_BRANCH": "migration-test",
		"HARNESS_PARAMS_FILE": filepath.Join(home, "missing-params.json"), "HARNESS_WORK_DIR": filepath.Join(home, "repo"),
		"HARNESS_TERMINATION_LOG_PATH": filepath.Join(home, "termination-log"),
	} {
		t.Setenv(key, value)
	}
	_, err := runStage(nil, nil)
	if err == nil || !strings.Contains(err.Error(), "clone:") || !strings.Contains(err.Error(), "none.git") {
		t.Fatalf("placeholder key should not corrupt the clone error: %v", err)
	}
	termination, _ := os.ReadFile(filepath.Join(home, "termination-log"))
	if !strings.Contains(string(termination), "none.git") {
		t.Error("placeholder key corrupted the termination log")
	}
}
