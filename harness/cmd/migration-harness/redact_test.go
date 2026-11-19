package main

import (
	"strings"
	"testing"
)

func TestRedactorMasksKnownAndEnvSecrets(t *testing.T) {
	t.Setenv("KONVEYOR_LLM_API_KEY", "sk-live-0123456789abcdef")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY")
	t.Setenv("HUB_TOKEN_ID", "7")           // credential-looking name, not a secret
	t.Setenv("TARGET_BRANCH", "konveyor/x") // ordinary variable

	r := newRedactor("hub-token-value-12345", "", "short")
	in := "I set KONVEYOR_LLM_API_KEY=sk-live-0123456789abcdef and the AWS key wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY " +
		"with hub token hub-token-value-12345 on branch konveyor/x, id 7, short"
	got := r.redact(in)
	for _, leaked := range []string{"sk-live-0123456789abcdef", "wJalrXUtnFEMI", "hub-token-value-12345"} {
		if strings.Contains(got, leaked) {
			t.Errorf("secret leaked: %q in %q", leaked, got)
		}
	}
	for _, kept := range []string{"konveyor/x", "id 7", "short", "KONVEYOR_LLM_API_KEY=[redacted]"} {
		if !strings.Contains(got, kept) {
			t.Errorf("expected %q to survive, got %q", kept, got)
		}
	}
}

// A secret that contains another must be masked as one token, not leave
// its remainder behind.
func TestRedactorLongestFirst(t *testing.T) {
	r := newRedactor("abcdefgh", "abcdefgh-and-more-12345")
	if got := r.redact("x abcdefgh-and-more-12345 y"); got != "x [redacted] y" {
		t.Fatalf("got %q", got)
	}
}

func TestRedactorAddedShortSecretsDoNotChangeReplacementMarkers(t *testing.T) {
	r := newRedactor("repository-password")
	r.addSecrets("acted", "s3!", "")
	if got, want := r.redact("repository-password, acted, s3!"), "[redacted], [redacted], [redacted]"; got != want {
		t.Fatalf("redact() = %q, want %q", got, want)
	}
}

func TestNilRedactorIsANoop(t *testing.T) {
	var r *redactor
	if got := r.redact("unchanged"); got != "unchanged" {
		t.Fatalf("got %q", got)
	}
}

const testPEMKey = `-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
QyNTUxOQAAACAVeryVpRdcxsA5cenmxM7D1vJvCN3GXPLDcHtVXfFBQw
-----END OPENSSH PRIVATE KEY-----`

// A private key never reaches a log line whole — anything that echoes
// one prints it a line at a time — so registering only the whole blob
// masks nothing that would actually leak.
func TestRedactorMasksPrivateKeyLines(t *testing.T) {
	r := newRedactor()
	r.addSecrets(privateKeyFragments(testPEMKey)...)

	lines := strings.Split(testPEMKey, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "-----") {
			continue
		}
		got := r.redact("agent said: " + line)
		if strings.Contains(got, line) {
			t.Errorf("key line leaked unmasked: %q", got)
		}
	}

	// The whole blob is still masked.
	if strings.Contains(r.redact(testPEMKey), "b3BlbnNzaC1rZXktdjEA") {
		t.Error("whole key leaked unmasked")
	}
}

// The PEM markers are identical across every key of a type, so masking
// them would redact ordinary prose while protecting nothing.
func TestRedactorKeepsPEMMarkers(t *testing.T) {
	r := newRedactor()
	r.addSecrets(privateKeyFragments(testPEMKey)...)

	msg := "the file must start with -----BEGIN OPENSSH PRIVATE KEY-----"
	if got := r.redact(msg); got != msg {
		t.Errorf("PEM marker was redacted: %q", got)
	}
}
