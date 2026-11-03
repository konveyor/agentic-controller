package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

const (
	// headerNameAPIKey is Anthropic's auth header, asserted absent for
	// OpenAI-compatible providers.
	headerNameAPIKey = "x-api-key"

	// headerAuthBearer is the OpenAI-compatible default auth header,
	// asserted absent for the providers that deviate from it.
	headerAuthBearer = "Authorization: Bearer"

	// Diagnostic fragments every probe writes to the termination log, so
	// the controller classifies both probes' outcomes the same way.
	diagOK          = "ok code=$code"
	diagUnreachable = "unreachable rc=$rc"
	diagAuthCase    = "401|403)"

	// testCredSecretName is the credentialRef secret name in table fixtures.
	testCredSecretName = "creds"
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
			wantNotContain: []string{headerAuthBearer},
		},
		{
			name:        "provider matching is case-insensitive and hyphen-normalized",
			provider:    "Anthropic",
			includeAuth: true,
			wantContains: []string{
				`x-api-key: $LLM_API_KEY`,
				endpointModelsProbe,
			},
			wantNotContain: []string{headerAuthBearer},
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
		testProviderVertex:  providerGCPVertex,
		testProviderBedrock: providerAWSBedrock,
		"AWS-Bedrock":       providerAWSBedrock,
	}
	for in, want := range cases {
		if got := normalizeProvider(in); got != want {
			t.Errorf("normalizeProvider(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestProviderProbeKind guards which probe each provider gets. aws-bedrock
// speaks SigV4 against bedrock-runtime, so it gets the ConverseStream probe;
// gcp-vertex-ai needs an OAuth2 token minted from a service account, which
// nothing in the Job can do, so it still gets no probe. Every other provider,
// including one the controller does not recognize, gets the OpenAI-compatible
// default.
func TestProviderProbeKind(t *testing.T) {
	cases := map[string]gatewayProbeKind{
		providerOpenAI:      probeModels,
		providerAnthropic:   probeModels,
		providerXAI:         probeModels,
		"some-new-provider": probeModels,
		providerAWSBedrock:  probeBedrockConverse,
		providerGCPVertex:   probeNone,
		// The mapping must survive the spelling users actually write.
		testProviderBedrock: probeBedrockConverse,
		"AWS-Bedrock":       probeBedrockConverse,
		testProviderVertex:  probeNone,
	}
	for provider, want := range cases {
		if got := providerProbeKind(provider); got != want {
			t.Errorf("providerProbeKind(%q) = %v, want %v", provider, got, want)
		}
	}
}

// TestNonDefaultProbeProvidersAreKnown guards that every provider given a
// non-default probe is still a provider the controller and harness recognize.
// A typo here would silently skip - or mis-probe - a provider that should
// have had the OpenAI-compatible default.
func TestNonDefaultProbeProvidersAreKnown(t *testing.T) {
	for provider := range providerProbes {
		if !knownProviders[provider] {
			t.Errorf("provider %q has a non-default probe but is not in knownProviders", provider)
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
		diagOK,
		"auth code=$code",
		"http code=$code",
		diagUnreachable,
		diagAuthCase,
	} {
		if !strings.Contains(withAuth, want) {
			t.Fatalf("expected %q in probe command %q", want, withAuth)
		}
	}

	// A keyless probe sends no credential, so 401/403 must not be classified
	// as an auth failure — otherwise we advise checking a key that isn't there.
	keyless := gatewayVerificationCurlCommand(providerOpenAI, false)
	for _, unwanted := range []string{"auth code=$code", diagAuthCase} {
		if strings.Contains(keyless, unwanted) {
			t.Fatalf("keyless command must not classify auth failures, found %q in %q", unwanted, keyless)
		}
	}
	// It still records reachability and generic HTTP outcomes.
	for _, want := range []string{diagOK, "http code=$code", diagUnreachable} {
		if !strings.Contains(keyless, want) {
			t.Fatalf("expected %q in keyless probe command %q", want, keyless)
		}
	}
}

// TestGatewayVerificationBedrockCommand guards the shape of the SigV4 probe:
// it must sign for bedrock-runtime, call the operation a run actually makes,
// cap the response to a single token, and classify the outcome the same way
// the models probe does - with AWS's error body carried as the detail tail.
func TestGatewayVerificationBedrockCommand(t *testing.T) {
	cmd := gatewayVerificationCommand(probeBedrockConverse, testProviderBedrock, false)

	for _, want := range []string{
		// SigV4, scoped to the region the run signs with.
		`--aws-sigv4 "aws:amz:$AWS_REGION:bedrock"`,
		`-u "$AWS_ACCESS_KEY_ID:$AWS_SECRET_ACCESS_KEY"`,
		// Streaming, because that is what goose calls and what an SCP
		// denying bedrock:InvokeModelWithResponseStream would refuse.
		"$LLM_ENDPOINT/model/$LLM_MODEL/converse-stream",
		`"inferenceConfig":{"maxTokens":1}`,
		// Diagnostic vocabulary shared with the models probe.
		"/dev/termination-log",
		diagOK,
		diagAuthCase,
		"'auth code=%s detail=%s\\n'",
		"'http code=%s detail=%s\\n'",
		diagUnreachable,
		// A session token is part of an STS credential, and an empty one
		// must not be signed and sent.
		`if [ -n "$AWS_SESSION_TOKEN" ];`,
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("expected %q in bedrock probe command %q", want, cmd)
		}
	}

	// Nothing from the spec, and no credential, may be interpolated into the
	// command string: both travel as env vars (-k is also gone, because an
	// AWS endpoint has a public certificate and a signed credential should
	// not be handed to whatever answers).
	for _, notWant := range []string{headerAuthBearer, endpointModelsProbe, "curl -sk"} {
		if strings.Contains(cmd, notWant) {
			t.Errorf("did not expect %q in bedrock probe command %q", notWant, cmd)
		}
	}
}

// TestIsBedrockRuntimeEndpoint guards the gate that decides where the probe
// is willing to send a signed AWS credential (#102). Anything that is not an
// AWS-operated bedrock-runtime address must be refused, not probed.
func TestIsBedrockRuntimeEndpoint(t *testing.T) {
	cases := map[string]bool{
		"https://bedrock-runtime.us-east-1.amazonaws.com":                true,
		"https://bedrock-runtime-fips.us-gov-west-1.amazonaws.com":       true,
		"https://bedrock-runtime.cn-north-1.amazonaws.com.cn":            true,
		"https://vpce-0a1b.bedrock-runtime.us-east-1.vpce.amazonaws.com": true,
		// Plain HTTP would send the signature in the clear.
		"http://bedrock-runtime.us-east-1.amazonaws.com": false,
		// A lookalike host that is not under an AWS domain.
		"https://bedrock-runtime.us-east-1.amazonaws.com.evil.example": false,
		"https://bedrock.example.com":                                  false,
		// An OpenAI-compatible proxy in front of Bedrock is a different
		// provider; signing an AWS credential for it is never right.
		"https://llm-proxy.svc.cluster.local": false,
		"https://169.254.169.254":             false,
		"":                                    false,
		"://not a url":                        false,
	}
	for endpoint, want := range cases {
		if got := isBedrockRuntimeEndpoint(endpoint); got != want {
			t.Errorf("isBedrockRuntimeEndpoint(%q) = %v, want %v", endpoint, got, want)
		}
	}
}

// TestGatewayProbeSkip guards every case where a Gateway is settled without
// being probed. Each one leaves it exactly where #167 left it - Ready and
// honestly unverified - rather than failing a configuration that works.
func TestGatewayProbeSkip(t *testing.T) {
	bedrockSecret := func(keys map[string]string) *corev1.Secret {
		data := map[string][]byte{}
		for k, v := range keys {
			data[k] = []byte(v)
		}
		return &corev1.Secret{Data: data}
	}
	fullKeys := map[string]string{
		awsAccessKeyIDEnv:     "AKIAEXAMPLE",
		awsSecretAccessKeyEnv: "secret",
		awsRegionEnv:          testAWSRegion,
	}
	withoutKey := func(drop string) map[string]string {
		out := map[string]string{}
		for k, v := range fullKeys {
			if k != drop {
				out[k] = v
			}
		}
		return out
	}

	gateway := func(provider, endpoint string) *konveyoriov1alpha1.Gateway {
		return &konveyoriov1alpha1.Gateway{
			Spec: konveyoriov1alpha1.GatewaySpec{
				Provider:      provider,
				Endpoint:      endpoint,
				CredentialRef: konveyoriov1alpha1.GatewayCredentialRef{SecretName: testCredSecretName},
			},
		}
	}
	const bedrockEndpoint = "https://bedrock-runtime.us-east-1.amazonaws.com"

	tests := []struct {
		name      string
		gateway   *konveyoriov1alpha1.Gateway
		secret    *corev1.Secret
		wantSkip  bool
		wantInMsg string
	}{
		{
			name:     "bedrock with complete static credentials is probed",
			gateway:  gateway(testProviderBedrock, bedrockEndpoint),
			secret:   bedrockSecret(fullKeys),
			wantSkip: false,
		},
		{
			name:      "vertex still has no probe",
			gateway:   gateway(testProviderVertex, "https://aiplatform.googleapis.com"),
			secret:    bedrockSecret(nil),
			wantSkip:  true,
			wantInMsg: "no API surface the verification probe can speak",
		},
		{
			// The common footgun: goose cannot reach Bedrock without it
			// either, and the message says which key is missing.
			name:      "bedrock without AWS_REGION names the missing key",
			gateway:   gateway(testProviderBedrock, bedrockEndpoint),
			secret:    bedrockSecret(withoutKey(awsRegionEnv)),
			wantSkip:  true,
			wantInMsg: awsRegionEnv,
		},
		{
			// IRSA or any other SDK credential source: a working setup the
			// probe pod cannot reproduce, so it must not be called a failure.
			name:      "bedrock without static keys is skipped, not failed",
			gateway:   gateway(testProviderBedrock, bedrockEndpoint),
			secret:    bedrockSecret(map[string]string{awsRegionEnv: testAWSRegion}),
			wantSkip:  true,
			wantInMsg: awsAccessKeyIDEnv,
		},
		{
			name:      "bedrock with an empty key value counts as missing",
			gateway:   gateway(testProviderBedrock, bedrockEndpoint),
			secret:    bedrockSecret(map[string]string{awsAccessKeyIDEnv: "a", awsSecretAccessKeyEnv: "b", awsRegionEnv: ""}),
			wantSkip:  true,
			wantInMsg: awsRegionEnv,
		},
		{
			name:      "bedrock pointed at a non-AWS host is never signed for",
			gateway:   gateway(testProviderBedrock, "https://attacker.example.com"),
			secret:    bedrockSecret(fullKeys),
			wantSkip:  true,
			wantInMsg: "will not sign an AWS credential for another host",
		},
		{
			name:     "openai is probed as before",
			gateway:  gateway(providerOpenAI, "https://api.openai.com"),
			secret:   bedrockSecret(nil),
			wantSkip: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe := providerProbeKind(tt.gateway.Spec.Provider)
			msg, skipped := gatewayProbeSkip(tt.gateway, probe, tt.secret)
			if skipped != tt.wantSkip {
				t.Fatalf("skipped = %v, want %v (message %q)", skipped, tt.wantSkip, msg)
			}
			if !skipped {
				return
			}
			if !strings.Contains(msg, tt.wantInMsg) {
				t.Errorf("message = %q, want to contain %q", msg, tt.wantInMsg)
			}
			if !strings.Contains(msg, "unverified") {
				t.Errorf("message = %q, want it to say the Gateway is unverified", msg)
			}
		})
	}
}

func TestGatewayVerifyReason(t *testing.T) {
	const ep = "https://api.example.com"

	tests := []struct {
		name       string
		diag       string
		succeeded  bool
		probe      gatewayProbeKind
		wantReason string
		wantInMsg  string
		notInMsg   string
	}{
		{"success with code", "ok code=200", true, probeModels, reasonConnectionVerified, "HTTP 200", ""},
		{"success no diag", "", true, probeModels, reasonConnectionVerified, "is reachable", "HTTP"},
		// A success verdict with a non-"ok" diagnostic must not quote the code,
		// or we'd report "is reachable (HTTP 401)".
		{"success mismatched diag", "auth code=401", true, probeModels, reasonConnectionVerified, "is reachable", "HTTP"},
		{"auth 401", "auth code=401", false, probeModels, reasonAuthenticationFailed, "HTTP 401", ""},
		{"auth 403", "auth code=403", false, probeModels, reasonAuthenticationFailed, "API key", ""},
		{"other non-2xx", "http code=500", false, probeModels, reasonConnectionFailed, "HTTP 500", ""},
		{"unreachable timeout", "unreachable rc=28", false, probeModels, reasonEndpointUnreachable, "timed out", ""},
		{"unreachable dns", "unreachable rc=6", false, probeModels, reasonEndpointUnreachable, "resolve host", ""},
		{"unreachable refused", "unreachable rc=7", false, probeModels, reasonEndpointUnreachable, "refused", ""},
		{"failure no diag", "", false, probeModels, reasonConnectionFailed, "failed", ""},
		{"unrecognized diag", "weird output", false, probeModels, reasonConnectionFailed, "failed", ""},

		// The Bedrock probe: a pass says the credential invoked the model, and
		// a failure carries AWS's own sentence - the whole point of #304, where
		// an SCP denial reached the user as a bare "Authentication required".
		{
			"bedrock success", "ok code=200", true, probeBedrockConverse,
			reasonConnectionVerified, "ConverseStream", "",
		},
		{
			"bedrock SCP denial names the operation",
			`auth code=403 detail={"message":"User: arn:aws:iam::1:user/u is not authorized to ` +
				`perform: bedrock:InvokeModelWithResponseStream on the inference profile x"}`,
			false, probeBedrockConverse,
			reasonAuthenticationFailed, "is not authorized to perform: bedrock:InvokeModelWithResponseStream", "",
		},
		{
			"bedrock invalid key",
			`auth code=403 detail={"message":"The security token included in the request is invalid."}`,
			false, probeBedrockConverse,
			reasonAuthenticationFailed, "The security token included in the request is invalid.", "/v1/models",
		},
		{
			"bedrock auth advice names the permission",
			"auth code=403", false, probeBedrockConverse,
			reasonAuthenticationFailed, "bedrock:InvokeModelWithResponseStream", "API key",
		},
		{
			"bedrock validation error keeps the body",
			`http code=400 detail={"message":"The provided model identifier is invalid."}`,
			false, probeBedrockConverse,
			reasonConnectionFailed, "The provided model identifier is invalid.", "",
		},
		{
			"bedrock throttling is a plain non-2xx",
			`http code=429 detail={"message":"Too many requests"}`,
			false, probeBedrockConverse, reasonConnectionFailed, "HTTP 429", "",
		},
		{
			"bedrock unreachable", "unreachable rc=6", false, probeBedrockConverse,
			reasonEndpointUnreachable, "resolve host", "",
		},
		// An empty detail= tail must not leave a dangling colon on the message.
		{
			"bedrock empty detail", "http code=502 detail=", false, probeBedrockConverse,
			reasonConnectionFailed, "HTTP 502", "502:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, msg := gatewayVerifyReason(tt.diag, ep, "gw-verify-x-gen1", tt.succeeded, tt.probe)
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
