package kai

import "testing"

// wantIssuerURL is the OIDC issuer derived from the "https://host" Hub base URLs.
const wantIssuerURL = "https://host/oidc"

func TestDeriveIssuerURL(t *testing.T) {
	tests := []struct {
		hubURL string
		want   string
	}{
		{"https://host/hub", wantIssuerURL},
		{"https://host/hub/", wantIssuerURL},
		{"https://host", wantIssuerURL},
		{"https://host/", wantIssuerURL},
		{"http://tackle-hub.konveyor-tackle.svc:8080", "http://tackle-hub.konveyor-tackle.svc:8080/oidc"},
	}
	for _, tt := range tests {
		if got := deriveIssuerURL(tt.hubURL); got != tt.want {
			t.Errorf("deriveIssuerURL(%q) = %q, want %q", tt.hubURL, got, tt.want)
		}
	}
}
