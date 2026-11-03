package kai

import "testing"

func TestBuildSecretData(t *testing.T) {
	aws, _ := lookupProvider("aws-bedrock")
	fields := credentialFieldsFor(aws, "")
	values := map[string]string{
		awsAccessKeyID:     "AKIA",
		awsSecretAccessKey: "shh",
		awsRegion:          "us-east-1",
		"EXTRA":            "ignored",
	}
	data := buildSecretData(fields, values)
	if len(data) != 3 {
		t.Fatalf("expected only the declared keys, got %d: %v", len(data), data)
	}
	for k, want := range map[string]string{
		awsAccessKeyID:     "AKIA",
		awsSecretAccessKey: "shh",
		awsRegion:          "us-east-1",
	} {
		if data[k] != want {
			t.Errorf("data[%q] = %q, want %q", k, data[k], want)
		}
	}
	if _, ok := data["EXTRA"]; ok {
		t.Error("buildSecretData should not include undeclared keys")
	}
}

func TestBuildSecretDataSingleKey(t *testing.T) {
	anthropic, _ := lookupProvider("anthropic")
	fields := credentialFieldsFor(anthropic, keyAPIKey)
	data := buildSecretData(fields, map[string]string{keyAPIKey: "sk-123"})
	if len(data) != 1 || data[keyAPIKey] != "sk-123" {
		t.Fatalf("expected single api-key entry, got %v", data)
	}
}
