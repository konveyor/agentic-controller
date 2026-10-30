package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunStageResolvesHubProxies(t *testing.T) {
	for _, stage := range []string{"standalone", "1", "2", "3"} {
		t.Run(stage, func(t *testing.T) {
			home := t.TempDir()
			var proxyLookups int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("Hub lookup must use the harness token")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/applications/42":
					_ = json.NewEncoder(w).Encode(map[string]any{"id": 42, "name": "proxy-app", "repository": map[string]string{"url": filepath.Join(home, "missing.git"), "branch": "main"}})
				case "/proxies":
					proxyLookups++
					_, _ = w.Write([]byte(`[{"id":1,"enabled":true,"kind":"http","host":"proxy.example.com","port":3128,"excluded":["*.internal.example.com"]}]`))
				default:
					_, _ = w.Write([]byte(`[]`))
				}
			}))
			defer server.Close()
			for key, value := range map[string]string{
				"HOME": home, "USERPROFILE": home, "KONVEYOR_LLM_MODEL": "test-model",
				"KONVEYOR_ACP_SECRET_KEY": "test-acp-key",
				"HUB_BASE_URL":            server.URL, "HUB_TOKEN": "test-token", "HUB_TOKEN_ID": "", "APP_ID": "42",
				"TARGET_BRANCH": "migration-test", "HARNESS_HUB_PROXY": "on",
				"HARNESS_PARAMS_FILE":          filepath.Join(home, "missing-params.json"),
				"HARNESS_WORK_DIR":             filepath.Join(home, "repo"),
				"HARNESS_TERMINATION_LOG_PATH": filepath.Join(home, "termination-log"),
				"KONVEYOR_WORKFLOW_STAGE":      "", "KONVEYOR_WORKFLOW_STAGE_COUNT": "",
			} {
				t.Setenv(key, value)
			}
			if stage != "standalone" {
				t.Setenv("KONVEYOR_WORKFLOW_STAGE", stage)
				t.Setenv("KONVEYOR_WORKFLOW_STAGE_COUNT", "3")
			}
			code, err := runStage(nil, nil)
			if code != 1 || err == nil || !strings.Contains(err.Error(), "clone:") {
				t.Fatalf("runStage = (%d, %v), want deliberate clone failure", code, err)
			}
			if proxyLookups != 1 {
				t.Errorf("Hub proxy lookups = %d, want 1 before clone and runtime startup", proxyLookups)
			}
			data, err := os.ReadFile(filepath.Join(home, ".m2", "settings.xml"))
			if err != nil {
				t.Fatalf("proxy-only Maven settings were not prepared: %v", err)
			}
			if !strings.Contains(string(data), "proxy.example.com") {
				t.Error("Maven settings do not include the Hub proxy")
			}
		})
	}
}

func TestRunStageProxyOptOutAndFailures(t *testing.T) {
	for _, tt := range []struct {
		name, switchValue, body string
		status                  int
		wantFailure             bool
	}{
		{name: "opt-out bypasses forbidden lookup", switchValue: "off", status: http.StatusForbidden},
		{name: "absent", body: `[]`},
		{name: "disabled skips invalid proxy and identity", body: `[{"id":1,"enabled":false,"kind":"http","identity":{"id":7}}]`},
		{name: "forbidden lookup", status: http.StatusForbidden, body: `{"password":"must-not-appear-in-errors"}`, wantFailure: true},
		{name: "forbidden identity", body: `[{"id":1,"enabled":true,"kind":"http","host":"proxy.example","identity":{"id":7}}]`, wantFailure: true},
		{name: "unsupported exclusion", body: `[{"id":1,"enabled":true,"kind":"http","host":"proxy.example","excluded":["10.0.0.0/8"]}]`, wantFailure: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			proxyCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/applications/42":
					_ = json.NewEncoder(w).Encode(map[string]any{"id": 42, "name": "proxy-app", "repository": map[string]string{"url": filepath.Join(home, "missing.git"), "branch": "main"}})
				case "/proxies":
					proxyCalls++
					if tt.status != 0 {
						w.WriteHeader(tt.status)
					}
					_, _ = w.Write([]byte(tt.body))
				case "/identities/7":
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"password":"must-not-appear-in-errors"}`))
				default:
					_, _ = w.Write([]byte(`[]`))
				}
			}))
			defer server.Close()
			for key, value := range map[string]string{
				"HOME": home, "USERPROFILE": home, "KONVEYOR_LLM_MODEL": "test-model", "KONVEYOR_ACP_SECRET_KEY": "test-acp-key",
				"HUB_BASE_URL": server.URL, "HUB_TOKEN": "test-token", "HUB_TOKEN_ID": "", "APP_ID": "42",
				"TARGET_BRANCH": "migration-test", "HARNESS_HUB_PROXY": tt.switchValue,
				"HARNESS_PARAMS_FILE": filepath.Join(home, "missing-params.json"), "HARNESS_WORK_DIR": filepath.Join(home, "repo"),
				"HARNESS_TERMINATION_LOG_PATH": filepath.Join(home, "termination-log"),
			} {
				t.Setenv(key, value)
			}
			code, err := runStage(nil, nil)
			if code != 1 || err == nil {
				t.Fatalf("expected deliberate startup failure, got (%d, %v)", code, err)
			}
			if tt.wantFailure {
				if !strings.Contains(err.Error(), "HARNESS_HUB_PROXY=off") || strings.Contains(err.Error(), "clone:") {
					t.Errorf("proxy setup must fail before clone and name opt-out: %v", err)
				}
			} else if !strings.Contains(err.Error(), "clone:") {
				t.Errorf("expected successful proxy setup then deliberate clone failure: %v", err)
			}
			if tt.switchValue == "off" && proxyCalls != 0 {
				t.Error("opt-out still queried Hub proxies")
			}
			if _, err := os.Stat(filepath.Join(home, ".m2", "settings.xml")); !os.IsNotExist(err) {
				t.Error("absent, disabled or opted-out proxy settings should not create Maven settings")
			}
			termination, _ := os.ReadFile(filepath.Join(home, "termination-log"))
			if strings.Contains(err.Error(), "must-not-appear-in-errors") || strings.Contains(string(termination), "must-not-appear-in-errors") {
				t.Error("proxy identity response leaked into diagnostics")
			}
		})
	}
}
