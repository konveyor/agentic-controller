/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
)

const (
	// testBedrockModel is a cross-region inference profile id; testBedrockARN
	// is the same model named by ARN, whose slashes are what break routing
	// unescaped and whose escaping is what breaks curl's own signing.
	testBedrockModel = "us.anthropic.claude-sonnet-4-5-20250929-v1:0"
	testBedrockARN   = "arn:aws:bedrock:us-east-1:123456789012:" +
		"inference-profile/us.anthropic.claude-sonnet-4-5-20250929-v1:0"
)

func testBedrockSecret(extra map[string]string) *corev1.Secret {
	data := map[string][]byte{
		awsAccessKeyIDEnv:     []byte(testAWSAccessKeyID),
		awsSecretAccessKeyEnv: []byte(testAWSSecretKey),
		awsRegionEnv:          []byte(testAWSRegion),
	}
	for k, v := range extra {
		data[k] = []byte(v)
	}
	return &corev1.Secret{Data: data}
}

// testSigningTime is fixed so the signatures below are golden values.
var testSigningTime = time.Date(2026, 11, 7, 12, 34, 56, 0, time.UTC)

// TestAWSSigV4SigningKeyVector pins the signing-key derivation against the
// published vector in AWS's own SigV4 documentation (iam, us-east-1,
// 20150830). It is the one part of the signer with an externally known answer,
// so it is the part that must not drift silently.
func TestAWSSigV4SigningKeyVector(t *testing.T) {
	const (
		secretKey = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
		want      = "c4afb1cc5771d871763a393e44b703571b55cc28424d1a5e86da6ed3c154a4b9"
	)
	key := []byte("AWS4" + secretKey)
	for _, part := range []string{"20150830", "us-east-1", "iam", "aws4_request"} {
		key = hmacSHA256(key, part)
	}
	if got := hex.EncodeToString(key); got != want {
		t.Errorf("signing key = %s, want %s (AWS published vector)", got, want)
	}
}

// TestAWSEscapeURIPath guards SigV4's canonical URI for every service but S3:
// the path as sent, URI-encoded once more. This is precisely what curl 8.12.1
// does not do - it signs the path verbatim - which is why the controller signs
// the request instead of curl.
func TestAWSEscapeURIPath(t *testing.T) {
	cases := map[string]string{
		// A colon already escaped on the wire escapes again: %3A -> %253A.
		"/model/us-v1%3A0/converse-stream": "/model/us-v1%253A0/converse-stream",
		// So does an escaped slash, which is how an ARN survives routing.
		"/model/arn%3Aaws%2Fx/converse-stream": "/model/arn%253Aaws%252Fx/converse-stream",
		// Separators are kept; unreserved characters are untouched.
		"/model/plain-model.id~1/converse-stream": "/model/plain-model.id~1/converse-stream",
		"/": "/",
	}
	for in, want := range cases {
		if got := awsEscapeURIPath(in); got != want {
			t.Errorf("awsEscapeURIPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSignBedrockProbeRequest pins the signed request for fixed inputs. The
// signatures are golden values, cross-checked against an independent
// implementation of SigV4 written from the specification, for both a bare
// inference-profile id and an ARN.
//
// The two signatures below were produced by that independent implementation,
// not copied from this code's output, so a change in the signer fails here
// rather than silently redefining the expectation.
//
// What this cannot establish is that AWS accepts them: a positive end-to-end
// check needs a credential that resolves, because AWS rejects an unknown
// access key before it validates any signature. That check belongs on a
// cluster with a real Secret.
func TestSignBedrockProbeRequest(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		wantURL  string
		wantAuth string
	}{
		{
			name:    "bare inference profile id",
			model:   testBedrockModel,
			wantURL: bedrockEndpoint + "/model/us.anthropic.claude-sonnet-4-5-20250929-v1%3A0/converse-stream",
			wantAuth: "AWS4-HMAC-SHA256 Credential=" + testAWSAccessKeyID +
				"/20261107/us-east-1/bedrock/aws4_request, " +
				"SignedHeaders=content-type;host;x-amz-date, " +
				"Signature=6921c423a5793a193b6c0ee615c6433ff6e6ce9bc4095b70516d6280ff3c02f1",
		},
		{
			name:  "inference profile ARN",
			model: testBedrockARN,
			wantURL: bedrockEndpoint + "/model/arn%3Aaws%3Abedrock%3Aus-east-1%3A123456789012%3A" +
				"inference-profile%2Fus.anthropic.claude-sonnet-4-5-20250929-v1%3A0/converse-stream",
			wantAuth: "AWS4-HMAC-SHA256 Credential=" + testAWSAccessKeyID +
				"/20261107/us-east-1/bedrock/aws4_request, " +
				"SignedHeaders=content-type;host;x-amz-date, " +
				"Signature=8638d5d36320661ffd7ae8ea41610506f196148ff1c5e181be54f9e9d4f25604",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := signBedrockProbeRequest(
				testBedrockSecret(nil), bedrockEndpoint, tt.model, testSigningTime)
			if err != nil {
				t.Fatalf("signBedrockProbeRequest: %v", err)
			}
			if got.URL != tt.wantURL {
				t.Errorf("URL = %q, want %q", got.URL, tt.wantURL)
			}
			if got.Authorization != tt.wantAuth {
				t.Errorf("Authorization = %q, want %q", got.Authorization, tt.wantAuth)
			}
			if got.AMZDate != "20261107T123456Z" {
				t.Errorf("AMZDate = %q, want 20261107T123456Z", got.AMZDate)
			}
			// The wire path is escaped once; the canonical path the signature
			// covers is escaped twice. A URL carrying the double-escaped form
			// would route to nothing.
			if strings.Contains(got.URL, "%253A") {
				t.Errorf("URL carries the canonical (double-escaped) path: %q", got.URL)
			}
			// The secret access key must never leave the controller.
			if strings.Contains(got.URL+got.Authorization+got.SecurityToken, testAWSSecretKey) {
				t.Error("signed request leaks the secret access key")
			}
		})
	}
}

// TestSignBedrockProbeRequestSessionToken guards temporary credentials: the
// session token is part of the credential, so it has to be a signed header and
// has to reach the pod, and its presence must change the signature.
func TestSignBedrockProbeRequestSessionToken(t *testing.T) {
	const token = "FwoGZXIvYXdzEEXAMPLETOKEN"

	plain, err := signBedrockProbeRequest(
		testBedrockSecret(nil), bedrockEndpoint, testBedrockModel, testSigningTime)
	if err != nil {
		t.Fatalf("signBedrockProbeRequest: %v", err)
	}
	withToken, err := signBedrockProbeRequest(
		testBedrockSecret(map[string]string{awsSessionTokenEnv: token}),
		bedrockEndpoint, testBedrockModel, testSigningTime)
	if err != nil {
		t.Fatalf("signBedrockProbeRequest: %v", err)
	}

	if withToken.SecurityToken != token {
		t.Errorf("SecurityToken = %q, want the Secret's token", withToken.SecurityToken)
	}
	if !strings.Contains(withToken.Authorization, "x-amz-security-token") {
		t.Errorf("x-amz-security-token is not a signed header: %q", withToken.Authorization)
	}
	if withToken.Authorization == plain.Authorization {
		t.Error("adding a session token did not change the signature")
	}
	if plain.SecurityToken != "" {
		t.Errorf("SecurityToken = %q for a static credential, want empty", plain.SecurityToken)
	}
}

// TestSignBedrockProbeRequestFollowsSecretEndpoint guards that the signature's
// host follows the Secret's endpoint override, since the probe has to sign for
// the host it actually calls (bedrockProbeEndpoint).
func TestSignBedrockProbeRequestFollowsSecretEndpoint(t *testing.T) {
	got, err := signBedrockProbeRequest(
		testBedrockSecret(nil), bedrockVPCEndpoint, testBedrockModel, testSigningTime)
	if err != nil {
		t.Fatalf("signBedrockProbeRequest: %v", err)
	}
	if !strings.HasPrefix(got.URL, bedrockVPCEndpoint+"/model/") {
		t.Errorf("URL = %q, want it under %q", got.URL, bedrockVPCEndpoint)
	}
	// Same region scope, different host, so a different signature.
	regional, err := signBedrockProbeRequest(
		testBedrockSecret(nil), bedrockEndpoint, testBedrockModel, testSigningTime)
	if err != nil {
		t.Fatalf("signBedrockProbeRequest: %v", err)
	}
	if got.Authorization == regional.Authorization {
		t.Error("the host is not covered by the signature")
	}
}

// TestSignBedrockProbeRequestBasePathOverride is the regression guard for the
// second endpoint finding: an override may carry a base path, and the
// signature has to cover the path actually sent. A proxy at
// https://proxy.example/bedrock sends /bedrock/model/..., and signing
// /model/... alone produced an Authorization header AWS could not match.
//
// The goldens come from an independent implementation of SigV4, as in
// TestSignBedrockProbeRequest.
func TestSignBedrockProbeRequestBasePathOverride(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		wantURL  string
		wantSig  string
	}{
		{
			name:     "base path is part of the signed path",
			endpoint: bedrockProxyEndpoint,
			wantURL: bedrockProxyEndpoint + "/model/" +
				"us.anthropic.claude-sonnet-4-5-20250929-v1%3A0/converse-stream",
			wantSig: "ab4329f481d0654a92d78b1e332e1a7217756542fcb08292f9aa1f3af1e57073",
		},
		{
			// A trailing slash must not produce // in the path, which would
			// be a different path and so a different signature.
			name:     "trailing slash normalizes to the same request",
			endpoint: bedrockProxyEndpoint + "/",
			wantURL: bedrockProxyEndpoint + "/model/" +
				"us.anthropic.claude-sonnet-4-5-20250929-v1%3A0/converse-stream",
			wantSig: "ab4329f481d0654a92d78b1e332e1a7217756542fcb08292f9aa1f3af1e57073",
		},
		{
			name:     "multi-segment base path",
			endpoint: "https://proxy.example/a/b",
			wantURL: "https://proxy.example/a/b/model/" +
				"us.anthropic.claude-sonnet-4-5-20250929-v1%3A0/converse-stream",
			wantSig: "6699e4c1da66733d13139c43fb83035e3ac48e68438347db8de6229dc20d27c3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := signBedrockProbeRequest(
				testBedrockSecret(nil), tt.endpoint, testBedrockModel, testSigningTime)
			if err != nil {
				t.Fatalf("signBedrockProbeRequest: %v", err)
			}
			if got.URL != tt.wantURL {
				t.Errorf("URL = %q, want %q", got.URL, tt.wantURL)
			}
			if !strings.HasSuffix(got.Authorization, "Signature="+tt.wantSig) {
				t.Errorf("Authorization = %q, want signature %s", got.Authorization, tt.wantSig)
			}
			if strings.Contains(got.URL, "//model/") {
				t.Errorf("URL has an empty path segment: %q", got.URL)
			}
		})
	}

	// The host-only cases the reviewer confirmed against the official AWS SDK
	// must be untouched by the base-path handling.
	for _, endpoint := range []string{bedrockEndpoint, bedrockVPCEndpoint} {
		got, err := signBedrockProbeRequest(
			testBedrockSecret(nil), endpoint, testBedrockModel, testSigningTime)
		if err != nil {
			t.Fatalf("signBedrockProbeRequest(%s): %v", endpoint, err)
		}
		if !strings.HasPrefix(got.URL, endpoint+"/model/") {
			t.Errorf("host-only override changed shape: %q", got.URL)
		}
	}
}

// TestSetBedrockEndpointVariants guards the first endpoint finding: goose
// honours the FIPS and dual-stack settings and each selects a different host
// from the plain regional one, so a Secret setting either is declined rather
// than probed against a host the run does not call. Reading an ambiguous value
// as "set" is deliberate - declining is the safe direction.
func TestSetBedrockEndpointVariants(t *testing.T) {
	tests := []struct {
		name string
		data map[string]string
		want []string
	}{
		{"unset", nil, nil},
		{"explicit false", map[string]string{
			awsUseFIPSEndpointEnv:      "false",
			awsUseDualStackEndpointEnv: "0",
		}, nil},
		{"empty counts as unset", map[string]string{awsUseFIPSEndpointEnv: "  "}, nil},
		{"fips on", map[string]string{awsUseFIPSEndpointEnv: awsFlagTrue}, []string{awsUseFIPSEndpointEnv}},
		{"dualstack on", map[string]string{awsUseDualStackEndpointEnv: "TRUE"},
			[]string{awsUseDualStackEndpointEnv}},
		{"both on", map[string]string{
			awsUseFIPSEndpointEnv:      awsFlagTrue,
			awsUseDualStackEndpointEnv: "1",
		}, []string{awsUseFIPSEndpointEnv, awsUseDualStackEndpointEnv}},
		// Anything unrecognized is treated as on, so the probe declines
		// instead of guessing which host the run resolved.
		{"unrecognized value", map[string]string{awsUseFIPSEndpointEnv: "yes"},
			[]string{awsUseFIPSEndpointEnv}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := setBedrockEndpointVariants(testBedrockSecret(tt.data))
			if len(got) != len(tt.want) {
				t.Fatalf("variants = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("variants = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// TestIsSignableEndpointOverride guards which override shapes the signer can
// reproduce byte for byte. A base path can be signed; a query, a fragment or
// embedded credentials cannot, and a signature that cannot match is worse than
// declining to probe.
func TestIsSignableEndpointOverride(t *testing.T) {
	cases := map[string]bool{
		bedrockEndpoint:                      true,
		bedrockProxyEndpoint:                 true,
		"https://proxy.example/a/b/":         true,
		"https://proxy.example:8443/bedrock": true,
		// Shapes the canonical request would have to model and this does not.
		bedrockProxyEndpoint + "?x=1":   false,
		bedrockProxyEndpoint + "#f":     false,
		"https://user:pw@proxy.example": false,
		// Still not in the clear, and still needs a host.
		"http://proxy.example/bedrock": false,
		"https:///bedrock":             false,
		"":                             false,
	}
	for override, want := range cases {
		if got := isSignableEndpointOverride(override); got != want {
			t.Errorf("isSignableEndpointOverride(%q) = %v, want %v", override, got, want)
		}
	}
}

// TestBedrockCommandCarriesNoSigningMaterial is the regression guard for the
// whole change: the probe pod replays a signature the controller computed, so
// the command must not reference the secret access key or curl's own signing,
// whose canonical URI is wrong on the curl this image ships.
func TestBedrockCommandCarriesNoSigningMaterial(t *testing.T) {
	cmd := gatewayVerificationCommand(probeBedrockConverse, testProviderBedrock, false)

	for _, unwanted := range []string{
		"--aws-sigv4",
		awsSecretAccessKeyEnv,
		awsAccessKeyIDEnv,
		"-u ",
	} {
		if strings.Contains(cmd, unwanted) {
			t.Errorf("bedrock probe command must not contain %q: %q", unwanted, cmd)
		}
	}
	for _, want := range []string{
		envProbeURL,
		envSigV4Authorization,
		envSigV4Date,
		envSigV4SecurityToken,
		"skew code=%s",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("expected %q in bedrock probe command %q", want, cmd)
		}
	}
}

// TestRedactEndpoint guards that an endpoint reaching a Gateway's status
// carries no credential. Status is readable by anyone who can read the
// Gateway, and the override shapes the probe declines - userinfo and query -
// are exactly the ones a token would be hidden in, with the skip message
// quoting the endpoint back.
func TestRedactEndpoint(t *testing.T) {
	cases := map[string]string{
		// Nothing to hide: returned untouched, so ordinary messages are
		// unchanged.
		bedrockEndpoint:      bedrockEndpoint,
		bedrockProxyEndpoint: bedrockProxyEndpoint,
		"":                   "",
		// Userinfo, the obvious one.
		"https://user:hunter2@proxy.example/bedrock": "https://" + redactedPlaceholder + "@proxy.example/bedrock",
		"https://token@proxy.example":                "https://" + redactedPlaceholder + "@proxy.example",
		// Query values go; keys stay, because the key is diagnostic and the
		// value is the secret.
		"https://proxy.example/bedrock?token=hunter2": "https://proxy.example/bedrock?token=" + redactedPlaceholder,
		"https://proxy.example?a=1&b=2":               "https://proxy.example?a=" + redactedPlaceholder + "&b=" + redactedPlaceholder,
		// Fragments can carry one too.
		"https://proxy.example/bedrock#hunter2": "https://proxy.example/bedrock#" + redactedPlaceholder,
	}
	for in, want := range cases {
		if got := redactEndpoint(in); got != want {
			t.Errorf("redactEndpoint(%q) = %q, want %q", in, got, want)
		}
	}

	// Whatever the shape, no secret survives.
	for _, in := range []string{
		"https://user:hunter2@proxy.example/bedrock?token=hunter2#hunter2",
		"https://proxy.example/bedrock?access_key=hunter2",
	} {
		if got := redactEndpoint(in); strings.Contains(got, "hunter2") {
			t.Errorf("redactEndpoint(%q) = %q, still carries the secret", in, got)
		}
	}
}

// TestProbeMessagesRedactTheEndpoint guards the path that actually reaches a
// user: a status message naming the endpoint must not reproduce a credential
// embedded in it.
func TestProbeMessagesRedactTheEndpoint(t *testing.T) {
	const leaky = "https://user:hunter2@proxy.example/bedrock?token=hunter2"

	for _, probe := range []gatewayProbeKind{probeModels, probeBedrockConverse} {
		for _, msg := range []string{
			probeSurface(probe, leaky),
			probeSuccessPhrase(probe, leaky),
		} {
			if strings.Contains(msg, "hunter2") {
				t.Errorf("status message leaks a credential: %q", msg)
			}
		}
		_, msg := gatewayVerifyReason("unreachable rc=6", leaky, "gw-verify-x", false, probe)
		if strings.Contains(msg, "hunter2") {
			t.Errorf("unreachable message leaks a credential: %q", msg)
		}
	}
}
