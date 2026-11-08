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
