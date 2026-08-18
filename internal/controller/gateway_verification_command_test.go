package controller

import (
	"strings"
	"testing"
)

func TestGatewayVerificationCurlCommand(t *testing.T) {
	withAuth := gatewayVerificationCurlCommand(true)
	if !strings.Contains(withAuth, verificationHTTPCodePattern) {
		t.Fatalf("expected %q in %q", verificationHTTPCodePattern, withAuth)
	}
	if !strings.Contains(withAuth, `Authorization: Bearer $LLM_API_KEY`) {
		t.Fatalf("expected Authorization header in %q", withAuth)
	}
	if !strings.Contains(withAuth, "$LLM_ENDPOINT/v1/models") {
		t.Fatalf("expected endpoint in %q", withAuth)
	}

	keyless := gatewayVerificationCurlCommand(false)
	if !strings.Contains(keyless, verificationHTTPCodePattern) {
		t.Fatalf("expected %q in %q", verificationHTTPCodePattern, keyless)
	}
	if strings.Contains(keyless, "Authorization") {
		t.Fatalf("keyless command should omit Authorization: %q", keyless)
	}
	if !strings.Contains(keyless, "$LLM_ENDPOINT/v1/models") {
		t.Fatalf("expected endpoint in %q", keyless)
	}
}
