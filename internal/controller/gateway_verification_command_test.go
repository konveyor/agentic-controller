package controller

import (
	"strings"
	"testing"
)

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
			provider:    "openai",
			includeAuth: true,
			wantContains: []string{
				verificationHTTPCodePattern,
				`Authorization: Bearer $LLM_API_KEY`,
				`$LLM_ENDPOINT/v1/models`,
			},
			wantNotContain: []string{"x-api-key"},
		},
		{
			name:        "xai falls to the OpenAI-compatible default",
			provider:    "xai",
			includeAuth: true,
			wantContains: []string{
				`Authorization: Bearer $LLM_API_KEY`,
				`$LLM_ENDPOINT/v1/models`,
			},
			wantNotContain: []string{"x-api-key"},
		},
		{
			name:        "anthropic uses x-api-key + anthropic-version against /v1/models",
			provider:    "anthropic",
			includeAuth: true,
			wantContains: []string{
				verificationHTTPCodePattern,
				`x-api-key: $LLM_API_KEY`,
				`anthropic-version: ` + anthropicAPIVersion,
				`$LLM_ENDPOINT/v1/models`,
			},
			wantNotContain: []string{`Authorization: Bearer`},
		},
		{
			name:        "provider matching is case-insensitive and hyphen-normalized",
			provider:    "Anthropic",
			includeAuth: true,
			wantContains: []string{
				`x-api-key: $LLM_API_KEY`,
				`$LLM_ENDPOINT/v1/models`,
			},
			wantNotContain: []string{`Authorization: Bearer`},
		},
		{
			name:        "keyless omits the auth header but keeps the provider path",
			provider:    "anthropic",
			includeAuth: false,
			wantContains: []string{
				verificationHTTPCodePattern,
				`$LLM_ENDPOINT/v1/models`,
			},
			wantNotContain: []string{"x-api-key", "Authorization"},
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
		"anthropic":     "anthropic",
		"Anthropic":     "anthropic",
		"gcp-vertex-ai": "gcp_vertex_ai",
		"aws-bedrock":   "aws_bedrock",
		"AWS-Bedrock":   "aws_bedrock",
	}
	for in, want := range cases {
		if got := normalizeProvider(in); got != want {
			t.Errorf("normalizeProvider(%q) = %q, want %q", in, got, want)
		}
	}
}
