package kai

import (
	"testing"
)

// testHubToken is a sample Hub personal access token used across runContext tests.
const testHubToken = "pat-xyz"

func TestRunContextBuild(t *testing.T) {
	// --app injects the full application context and defaults the git Secret.
	rc := runContext{
		appID: "10", hubBaseURL: defaultHubBaseURL, gitSecret: defaultGitSecret,
		targetBranch: "konveyor/triage-177",
	}
	env, envFrom, err := rc.build(false)
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
	if len(envFrom) != 1 || envFrom[0].SecretRef == nil || envFrom[0].SecretRef.Name != defaultGitSecret {
		t.Errorf("envFrom = %#v, want single secretRef %q", envFrom, defaultGitSecret)
	}
}

func TestRunContextBuildHubToken(t *testing.T) {
	// hubToken + app ⇒ HUB_TOKEN is injected.
	rc := runContext{appID: "10", hubBaseURL: defaultHubBaseURL, hubToken: testHubToken}
	env, _, err := rc.build(false)
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
	env, _, err = rc.build(false)
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
	rc := runContext{gitSecret: defaultGitSecret}
	env, envFrom, err := rc.build(false)
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

func TestRunContextBuildExplicitGitSecretWithoutApp(t *testing.T) {
	// An explicit --git-secret is honored even without --app.
	rc := runContext{gitSecret: "my-creds"}
	_, envFrom, err := rc.build(true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(envFrom) != 1 || envFrom[0].SecretRef.Name != "my-creds" {
		t.Errorf("envFrom = %#v, want single secretRef my-creds", envFrom)
	}
}

func TestRunContextBuildEmptyGitSecretSkips(t *testing.T) {
	// --git-secret "" with --app skips the git Secret but keeps app context.
	rc := runContext{appID: "5", gitSecret: ""}
	env, envFrom, err := rc.build(true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(env) == 0 {
		t.Error("expected APP_ID env")
	}
	if len(envFrom) != 0 {
		t.Errorf("envFrom = %#v, want empty", envFrom)
	}
}

func TestRunContextBuildExtraEnvAndEnvFrom(t *testing.T) {
	rc := runContext{
		env:     []string{"HUB_TOKEN=abc", "HUB_TOKEN_ID=7"},
		envFrom: []string{"extra-secret"},
		// no app, no default git secret opt-in
	}
	env, envFrom, err := rc.build(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(env) != 2 || env[0].Name != hubTokenEnvVar || env[1].Value != "7" {
		t.Errorf("env = %#v", env)
	}
	if len(envFrom) != 1 || envFrom[0].SecretRef.Name != "extra-secret" {
		t.Errorf("envFrom = %#v", envFrom)
	}

	if _, _, err := (&runContext{env: []string{"noequals"}}).build(false); err == nil {
		t.Error("expected error for --env without '='")
	}
}
