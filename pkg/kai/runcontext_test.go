package kai

import (
	"testing"
)

// testHubToken is a sample Hub personal access token used across runContext tests.
const testHubToken = "pat-xyz"

func TestRunContextBuild(t *testing.T) {
	// --app injects the full application context. It never wires a credential
	// Secret implicitly: the harness fetches source-control credentials from Hub.
	rc := runContext{
		appID: "10", hubBaseURL: defaultHubBaseURL,
		targetBranch: "konveyor/triage-177",
	}
	env, envFrom, err := rc.build()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := map[string]string{}
	for _, e := range env {
		got[e.Name] = e.Value
	}
	if got["APP_ID"] != "10" {
		t.Errorf("APP_ID = %q, want 10", got["APP_ID"])
	}
	if got["HUB_BASE_URL"] != defaultHubBaseURL {
		t.Errorf("HUB_BASE_URL = %q, want %q", got["HUB_BASE_URL"], defaultHubBaseURL)
	}
	if got["TARGET_BRANCH"] != "konveyor/triage-177" {
		t.Errorf("TARGET_BRANCH = %q", got["TARGET_BRANCH"])
	}
	if len(envFrom) != 0 {
		t.Errorf("envFrom = %#v, want empty (no implicit credential Secret)", envFrom)
	}
}

func TestRunContextBuildHubToken(t *testing.T) {
	// hubToken + app ⇒ HUB_TOKEN is injected.
	rc := runContext{appID: "10", hubBaseURL: defaultHubBaseURL, hubToken: testHubToken}
	env, _, err := rc.build()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := map[string]string{}
	for _, e := range env {
		got[e.Name] = e.Value
	}
	if got[hubTokenEnvVar] != testHubToken {
		t.Errorf("HUB_TOKEN = %q, want pat-xyz", got[hubTokenEnvVar])
	}

	// hubToken without app ⇒ no HUB_TOKEN (hub context is app-scoped).
	rc = runContext{hubToken: testHubToken}
	env, _, err = rc.build()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range env {
		if e.Name == hubTokenEnvVar {
			t.Errorf("HUB_TOKEN should not be set without --app, got %q", e.Value)
		}
	}
}

func TestRunContextBuildNoAppStaysClean(t *testing.T) {
	// Without --app and without an explicit --git-secret, nothing is injected.
	rc := runContext{}
	env, envFrom, err := rc.build()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(env) != 0 {
		t.Errorf("env = %#v, want empty", env)
	}
	if len(envFrom) != 0 {
		t.Errorf("envFrom = %#v, want empty", envFrom)
	}
}

func TestRunContextBuildExplicitGitSecret(t *testing.T) {
	// An explicit --git-secret is honored, with or without --app, and is the
	// only way a credential Secret is imported.
	for _, appID := range []string{"", "10"} {
		rc := runContext{appID: appID, gitSecret: "my-creds"}
		_, envFrom, err := rc.build()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(envFrom) != 1 || envFrom[0].SecretRef == nil || envFrom[0].SecretRef.Name != "my-creds" {
			t.Errorf("appID=%q: envFrom = %#v, want single secretRef my-creds", appID, envFrom)
		}
	}
}

func TestRunContextBuildExtraEnvAndEnvFrom(t *testing.T) {
	rc := runContext{
		env:     []string{"HUB_TOKEN=abc", "HUB_TOKEN_ID=7"},
		envFrom: []string{"extra-secret"},
		// no app, no git secret opt-in
	}
	env, envFrom, err := rc.build()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(env) != 2 || env[0].Name != hubTokenEnvVar || env[1].Value != "7" {
		t.Errorf("env = %#v", env)
	}
	if len(envFrom) != 1 || envFrom[0].SecretRef.Name != "extra-secret" {
		t.Errorf("envFrom = %#v", envFrom)
	}

	if _, _, err := (&runContext{env: []string{"noequals"}}).build(); err == nil {
		t.Error("expected error for --env without '='")
	}
}
