package kai

import (
	"strings"
	"testing"
)

// testSecretName is a placeholder credential Secret name used across tests.
const testSecretName = "creds"

func TestValidateProvider(t *testing.T) {
	for _, id := range providerIDs() {
		if err := validateProvider(id); err != nil {
			t.Errorf("expected provider %q to be valid, got %v", id, err)
		}
	}
	if err := validateProvider("does-not-exist"); err == nil {
		t.Error("expected error for unsupported provider")
	}
}

func TestValidateEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"https", "https://api.anthropic.com", false},
		{"http rejected", "http://localhost:8080", true},
		{"empty", "", true},
		{"no scheme", "api.anthropic.com", true},
		{"ftp scheme", "ftp://example.com", true},
		{"no host", "https://", true},
		{"userinfo rejected", "https://user:token@example.com", true},
		{"username-only userinfo rejected", "https://user@example.com", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEndpoint(tt.in)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateEndpoint(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
		})
	}
}

// TestDefaultEndpointsProbeURL guards the Gateway-verification contract: the
// controller probes "<endpoint>/v1/models", so a provider default endpoint must
// be a valid https URL and must not already end in "/v1" (which would produce a
// doubled "/v1/v1/models"). This is the regression that made the OpenAI default
// "https://api.openai.com" rather than ".../v1".
func TestDefaultEndpointsProbeURL(t *testing.T) {
	for _, p := range supportedProviders {
		if err := validateEndpoint(p.DefaultEndpoint); err != nil {
			t.Errorf("provider %q default endpoint %q is invalid: %v", p.ID, p.DefaultEndpoint, err)
		}
		if strings.HasSuffix(strings.TrimRight(p.DefaultEndpoint, "/"), "/v1") {
			t.Errorf("provider %q default endpoint %q ends in /v1; verification would probe a doubled /v1/v1/models",
				p.ID, p.DefaultEndpoint)
		}
	}
}

func TestValidateCredentialRef(t *testing.T) {
	single, _ := lookupProvider("anthropic")
	multi, _ := lookupProvider("aws-bedrock")

	tests := []struct {
		name       string
		provider   providerInfo
		secretName string
		key        string
		wantErr    bool
	}{
		{"single with key", single, testSecretName, keyAPIKey, false},
		{"single missing key", single, testSecretName, "", true},
		{"single missing secret", single, "", keyAPIKey, true},
		{"multi without key", multi, testSecretName, "", false},
		{"multi with key rejected", multi, testSecretName, keyAPIKey, true},
		{"multi missing secret", multi, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCredentialRef(tt.provider, tt.secretName, tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateCredentialRef() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCredentialFieldsFor(t *testing.T) {
	single, _ := lookupProvider("anthropic")
	got := credentialFieldsFor(single, "my-key")
	if len(got) != 1 || got[0].SecretKey != "my-key" {
		t.Fatalf("single-key: expected one field stored under the chosen key, got %+v", got)
	}
	if got[0].FromFile {
		t.Errorf("single-key API key should not be a file field")
	}

	gcp, _ := lookupProvider("gcp-vertex-ai")
	got = credentialFieldsFor(gcp, "")
	if len(got) != 1 || got[0].SecretKey != "GOOGLE_APPLICATION_CREDENTIALS_JSON" || !got[0].FromFile {
		t.Fatalf("gcp-vertex-ai: expected a single FromFile ADC field, got %+v", got)
	}

	aws, _ := lookupProvider("aws-bedrock")
	got = credentialFieldsFor(aws, "")
	want := []string{awsAccessKeyID, awsSecretAccessKey, awsRegion}
	if len(got) != len(want) {
		t.Fatalf("aws-bedrock: expected %d fields, got %d", len(want), len(got))
	}
	for i, k := range want {
		if got[i].SecretKey != k {
			t.Errorf("aws-bedrock field %d = %q, want %q", i, got[i].SecretKey, k)
		}
	}
}

func TestXAIProvider(t *testing.T) {
	p, ok := lookupProvider("xai")
	if !ok {
		t.Fatal("expected xai provider in the registry")
	}
	if p.CredentialMode != credentialModeSingleKey {
		t.Errorf("xai should be single-key, got %q", p.CredentialMode)
	}
	if err := validateProvider("xai"); err != nil {
		t.Errorf("validateProvider(xai) = %v", err)
	}
	if err := validateCredentialRef(p, testSecretName, keyAPIKey); err != nil {
		t.Errorf("validateCredentialRef(xai) = %v", err)
	}
}

func TestMissingSecretKeys(t *testing.T) {
	multi, _ := lookupProvider("aws-bedrock")
	present := map[string][]byte{awsAccessKeyID: []byte("x")}
	missing := missingSecretKeys(multi, present)
	if len(missing) != 2 {
		t.Fatalf("expected 2 missing keys, got %v", missing)
	}

	single, _ := lookupProvider("anthropic")
	if got := missingSecretKeys(single, nil); got != nil {
		t.Errorf("single-key provider should report no required keys, got %v", got)
	}
}
