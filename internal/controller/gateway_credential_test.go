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
	"crypto/sha256"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

// Fixtures for the hash table below; goconst objects to repeating them inline.
const (
	credOriginal = "sk-original"
	credRotated  = "sk-rotated"
	awsKeyID     = "AWS_ACCESS_KEY_ID"
	awsRegion    = "AWS_REGION"
)

func credentialSecret(data map[string]string) *corev1.Secret {
	s := &corev1.Secret{Data: map[string][]byte{}}
	for k, v := range data {
		s.Data[k] = []byte(v)
	}
	return s
}

func TestCredentialHash(t *testing.T) {
	keyed := konveyoriov1alpha1.GatewayCredentialRef{SecretName: "s", Key: testSecretKey}
	keyless := konveyoriov1alpha1.GatewayCredentialRef{SecretName: "s"}

	tests := []struct {
		name    string
		refA    konveyoriov1alpha1.GatewayCredentialRef
		secretA map[string]string
		refB    konveyoriov1alpha1.GatewayCredentialRef
		secretB map[string]string
		wantEq  bool
	}{
		{
			name: "same credential hashes the same",
			refA: keyed, secretA: map[string]string{testSecretKey: credOriginal},
			refB: keyed, secretB: map[string]string{testSecretKey: credOriginal},
			wantEq: true,
		},
		{
			name: "rotating the referenced key changes the hash",
			refA: keyed, secretA: map[string]string{testSecretKey: credOriginal},
			refB: keyed, secretB: map[string]string{testSecretKey: credRotated},
			wantEq: false,
		},
		{
			name: "an unrelated key in a shared Secret does not change the hash",
			refA: keyed, secretA: map[string]string{testSecretKey: credOriginal},
			refB: keyed, secretB: map[string]string{testSecretKey: credOriginal, "other": "x"},
			wantEq: true,
		},
		{
			name: "a missing referenced key differs from a present one",
			refA: keyed, secretA: map[string]string{testSecretKey: credOriginal},
			refB: keyed, secretB: map[string]string{"other": credOriginal},
			wantEq: false,
		},
		{
			name: "keyless covers every key in the Secret",
			refA: keyless, secretA: map[string]string{awsKeyID: "a", awsRegion: "us-east-1"},
			refB: keyless, secretB: map[string]string{awsKeyID: "a", awsRegion: "us-west-2"},
			wantEq: false,
		},
		{
			name: "keyless is stable across map iteration order",
			refA: keyless, secretA: map[string]string{"a": "1", "b": "2", "c": "3"},
			refB: keyless, secretB: map[string]string{"c": "3", "b": "2", "a": "1"},
			wantEq: true,
		},
		{
			// Without length prefixing these concatenate to the same bytes.
			name: "keyless does not confuse a split between key and value",
			refA: keyless, secretA: map[string]string{"ab": "c"},
			refB: keyless, secretB: map[string]string{"a": "bc"},
			wantEq: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := credentialHash(tt.refA, credentialSecret(tt.secretA))
			b := credentialHash(tt.refB, credentialSecret(tt.secretB))
			if (a == b) != tt.wantEq {
				t.Errorf("credentialHash: got %q and %q, want equal=%v", a, b, tt.wantEq)
			}
		})
	}
}

func TestVerificationJobNameVariesWithCredential(t *testing.T) {
	gateway := &konveyoriov1alpha1.Gateway{}
	gateway.Name = "gw"
	gateway.Generation = 1

	before := verificationJobName(gateway, "aaaaaaaa")
	after := verificationJobName(gateway, "bbbbbbbb")
	if before == after {
		t.Fatalf("both credentials produced %q; a rotation would read a stale Job's result", before)
	}

	gateway.Generation = 2
	if next := verificationJobName(gateway, "aaaaaaaa"); next == before {
		t.Fatalf("both generations produced %q; a spec change would reuse a stale Job", before)
	}
}

func TestCredentialHashKeepsTheFullDigest(t *testing.T) {
	// status.verifiedCredentialHash has no length limit, and it is what the
	// skip decision compares - only the Job name needs the short form.
	ref := konveyoriov1alpha1.GatewayCredentialRef{SecretName: "s", Key: testSecretKey}
	got := credentialHash(ref, credentialSecret(map[string]string{testSecretKey: credOriginal}))

	if len(got) != sha256.Size*2 {
		t.Errorf("credentialHash returned %d hex characters, want %d: %q",
			len(got), sha256.Size*2, got)
	}
	if short := shortCredentialHash(got); short != got[:8] {
		t.Errorf("shortCredentialHash(%q) = %q, want %q", got, short, got[:8])
	}
}

func TestShortCredentialHashHandlesAShortInput(t *testing.T) {
	if got := shortCredentialHash("abc"); got != "abc" {
		t.Errorf("shortCredentialHash(\"abc\") = %q, want \"abc\"", got)
	}
}

func TestGatewayLabelValueFitsALabelValue(t *testing.T) {
	// A Gateway name is a DNS subdomain (up to 253), a label value caps at 63.
	// Passing the raw name through has the API server reject the Job on create
	// and reject the selector that looks it up again.
	long := &konveyoriov1alpha1.Gateway{}
	long.Name = strings.Repeat("g", 64)

	got := gatewayLabelValue(long)
	if errs := validation.IsValidLabelValue(got); len(errs) > 0 {
		t.Errorf("gatewayLabelValue returned %q (len %d), not a valid label value: %v",
			got, len(got), errs)
	}

	// Two long names must not collapse onto one label, or the cleanup would
	// delete another Gateway's verification Job.
	other := &konveyoriov1alpha1.Gateway{}
	other.Name = strings.Repeat("g", 63) + "h"
	if next := gatewayLabelValue(other); next == got {
		t.Errorf("two Gateway names collapsed onto the label value %q", got)
	}

	// A name that already fits is passed through unchanged, so the label stays
	// readable for the overwhelmingly common case.
	short := &konveyoriov1alpha1.Gateway{}
	short.Name = "my-gateway"
	if got := gatewayLabelValue(short); got != "my-gateway" {
		t.Errorf("gatewayLabelValue(%q) = %q, want it unchanged", short.Name, got)
	}
}

func TestVerificationJobNameFitsALabelValue(t *testing.T) {
	// The Job controller copies the Job name into batch.kubernetes.io/job-name
	// on every pod, so a name over 63 characters makes the pods unschedulable.
	long := &konveyoriov1alpha1.Gateway{}
	long.Name = strings.Repeat("g", 200)
	long.Generation = 1

	name := verificationJobName(long, "aaaaaaaa")
	if len(name) > 63 {
		t.Errorf("verificationJobName returned %d characters, want at most 63: %q", len(name), name)
	}
	if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
		t.Errorf("verificationJobName returned %q, not a valid label: %v", name, errs)
	}

	// Truncation must not collapse two distinct verifications into one name.
	other := &konveyoriov1alpha1.Gateway{}
	other.Name = long.Name
	other.Generation = 2
	if next := verificationJobName(other, "aaaaaaaa"); next == name {
		t.Errorf("truncation collapsed two generations onto %q", name)
	}
	if rotated := verificationJobName(long, "bbbbbbbb"); rotated == name {
		t.Errorf("truncation collapsed two credentials onto %q", name)
	}
}
