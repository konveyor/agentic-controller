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

func TestNilRedactorIsANoop(t *testing.T) {
	var r *redactor
	if got := r.redact("unchanged"); got != "unchanged" {
		t.Fatalf("got %q", got)
	}
}
