package controller

import (
	"strings"
	"testing"
)

// headerNameAPIKey is Anthropic's auth header, asserted absent for
// OpenAI-compatible providers.
const headerNameAPIKey = "x-api-key"

func TestGatewayVerificationCurlCommand(t *testing.T) {
	tests := []struct {
		name           string
		provider       string
		includeAuth    bool
		wantContains   []string
		wantNotContain []string
	}{
		{
			name:        "default provider uses Authorization: Bearer against /v1/models",
			provider:    providerOpenAI,
			includeAuth: true,
			wantContains: []string{
				verificationHTTPCodePattern,
				`Authorization: Bearer $LLM_API_KEY`,
				endpointModelsProbe,
			},
			wantNotContain: []string{headerNameAPIKey},
		},
		{
			name:        "xai falls to the OpenAI-compatible default",
			provider:    providerXAI,
			includeAuth: true,
			wantContains: []string{
				`Authorization: Bearer $LLM_API_KEY`,
				endpointModelsProbe,
			},
			wantNotContain: []string{headerNameAPIKey},
		},
		{
			name:        "anthropic uses x-api-key + anthropic-version against /v1/models",
			provider:    providerAnthropic,
			includeAuth: true,
			wantContains: []string{
				verificationHTTPCodePattern,
				`x-api-key: $LLM_API_KEY`,
				`anthropic-version: ` + anthropicAPIVersion,
				endpointModelsProbe,
			},
			wantNotContain: []string{`Authorization: Bearer`},
		},
		{
			name:        "provider matching is case-insensitive and hyphen-normalized",
			provider:    "Anthropic",
			includeAuth: true,
			wantContains: []string{
				`x-api-key: $LLM_API_KEY`,
				endpointModelsProbe,
			},
			wantNotContain: []string{`Authorization: Bearer`},
		},
		{
			name:        "keyless omits the auth header but keeps the probe path",
			provider:    providerAnthropic,
			includeAuth: false,
			wantContains: []string{
				verificationHTTPCodePattern,
				endpointModelsProbe,
			},
			wantNotContain: []string{headerNameAPIKey, "Authorization"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := gatewayVerificationCurlCommand(tt.provider, tt.includeAuth)
			for _, want := range tt.wantContains {
				if !strings.Contains(cmd, want) {
					t.Errorf("expected %q in %q", want, cmd)
				}
			}
			for _, notWant := range tt.wantNotContain {
				if strings.Contains(cmd, notWant) {
					t.Errorf("did not expect %q in %q", notWant, cmd)
				}
			}
		})
	}
}

// TestNormalizeProvider guards that provider normalization stays in lockstep
// with the harness (providerEnv lowercases and converts hyphens to
// underscores), so gcp-vertex-ai/aws-bedrock match the same keys in both.
func TestNormalizeProvider(t *testing.T) {
	cases := map[string]string{
		providerAnthropic:   providerAnthropic,
		"Anthropic":         providerAnthropic,
		"gcp-vertex-ai":     providerGCPVertex,
		testProviderBedrock: providerAWSBedrock,
		"AWS-Bedrock":       providerAWSBedrock,
	}
	for in, want := range cases {
		if got := normalizeProvider(in); got != want {
			t.Errorf("normalizeProvider(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestHasModelsProbe guards which providers the controller will probe at all.
// aws-bedrock and gcp-vertex-ai reach their service through a cloud SDK, so
// there is no /v1/models to GET and no bearer token to send; probing them can
// only fail. Every other provider, including one the controller does not
// recognize, still gets the OpenAI-compatible probe.
func TestHasModelsProbe(t *testing.T) {
	cases := map[string]bool{
		providerOpenAI:      true,
		providerAnthropic:   true,
		providerXAI:         true,
		"some-new-provider": true,
		providerAWSBedrock:  false,
		providerGCPVertex:   false,
		// The skip must survive the spelling users actually write.
		testProviderBedrock: false,
		"AWS-Bedrock":       false,
		"gcp-vertex-ai":     false,
	}
	for provider, want := range cases {
		if got := hasModelsProbe(provider); got != want {
			t.Errorf("hasModelsProbe(%q) = %v, want %v", provider, got, want)
		}
	}
}

// TestSkippedProvidersAreKnown guards that every provider excluded from the
// probe is still a provider the controller and harness recognize. A typo here
// would silently skip verification for a provider that should have been
// probed.
func TestSkippedProvidersAreKnown(t *testing.T) {
	for provider := range providersWithoutModelsProbe {
		if !knownProviders[provider] {
			t.Errorf("provider %q is skipped for verification but is not in knownProviders", provider)
		}
	}
}

// TestGatewayVerificationProbeDiagnostic guards that the probe records a
// diagnostic in the pod termination message and classifies the outcome so the
// controller can surface the failure cause on the Gateway status.
func TestGatewayVerificationProbeDiagnostic(t *testing.T) {
	withAuth := gatewayVerificationCurlCommand(providerOpenAI, true)
	for _, want := range []string{
		"/dev/termination-log",
		"ok code=$code",
		"auth code=$code",
		"http code=$code",
		"unreachable rc=$rc",
		"401|403)",
	} {
		if !strings.Contains(withAuth, want) {
			t.Fatalf("expected %q in probe command %q", want, withAuth)
		}
	}

	// A keyless probe sends no credential, so 401/403 must not be classified
	// as an auth failure — otherwise we advise checking a key that isn't there.
	keyless := gatewayVerificationCurlCommand(providerOpenAI, false)
	for _, unwanted := range []string{"auth code=$code", "401|403)"} {
		if strings.Contains(keyless, unwanted) {
			t.Fatalf("keyless command must not classify auth failures, found %q in %q", unwanted, keyless)
		}
	}
	// It still records reachability and generic HTTP outcomes.
	for _, want := range []string{"ok code=$code", "http code=$code", "unreachable rc=$rc"} {
		if !strings.Contains(keyless, want) {
			t.Fatalf("expected %q in keyless probe command %q", want, keyless)
		}
	}
}

func TestGatewayVerifyReason(t *testing.T) {
	const ep = "https://api.example.com"

	tests := []struct {
		name       string
		diag       string
		succeeded  bool
		wantReason string
		wantInMsg  string
		notInMsg   string
	}{
		{"success with code", "ok code=200", true, reasonConnectionVerified, "HTTP 200", ""},
		{"success no diag", "", true, reasonConnectionVerified, "is reachable", "HTTP"},
		// A success verdict with a non-"ok" diagnostic must not quote the code,
		// or we'd report "is reachable (HTTP 401)".
		{"success mismatched diag", "auth code=401", true, reasonConnectionVerified, "is reachable", "HTTP"},
		{"auth 401", "auth code=401", false, reasonAuthenticationFailed, "HTTP 401", ""},
		{"auth 403", "auth code=403", false, reasonAuthenticationFailed, "API key", ""},
		{"other non-2xx", "http code=500", false, reasonConnectionFailed, "HTTP 500", ""},
		{"unreachable timeout", "unreachable rc=28", false, reasonEndpointUnreachable, "timed out", ""},
		{"unreachable dns", "unreachable rc=6", false, reasonEndpointUnreachable, "resolve host", ""},
		{"unreachable refused", "unreachable rc=7", false, reasonEndpointUnreachable, "refused", ""},
		{"failure no diag", "", false, reasonConnectionFailed, "failed", ""},
		{"unrecognized diag", "weird output", false, reasonConnectionFailed, "failed", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, msg := gatewayVerifyReason(tt.diag, ep, "gw-verify-x-gen1", tt.succeeded)
			if reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", reason, tt.wantReason)
			}
			if !strings.Contains(msg, tt.wantInMsg) {
				t.Errorf("message = %q, want to contain %q", msg, tt.wantInMsg)
			}
			if tt.notInMsg != "" && strings.Contains(msg, tt.notInMsg) {
				t.Errorf("message = %q, want NOT to contain %q", msg, tt.notInMsg)
			}
		})
	}
}
