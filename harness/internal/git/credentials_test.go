package git

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	cryptossh "golang.org/x/crypto/ssh"
)

func TestAuthReturnsNilForEmptyCreds(t *testing.T) {
	c := &Credentials{RepoURL: "https://github.com/acme/app.git"}
	auth, err := c.Auth()
	if err != nil {
		t.Fatalf("Auth() error = %v", err)
	}
	if auth != nil {
		t.Error("Auth() should return nil when both Username and Token are empty")
	}
}

func TestAuthReturnsBasicAuthWithCreds(t *testing.T) {
	c := &Credentials{RepoURL: "https://github.com/acme/app.git", Username: "user", Token: "token"}
	got, err := c.Auth()
	if err != nil {
		t.Fatalf("Auth() error = %v", err)
	}
	auth, ok := got.(*http.BasicAuth)
	if !ok {
		t.Fatal("Auth() should return a *http.BasicAuth")
	}
	if auth.Username != "user" {
		t.Errorf("Username = %q, want %q", auth.Username, "user")
	}
	if auth.Password != "token" {
		t.Errorf("Password = %q, want %q", auth.Password, "token")
	}
}

func TestAuthReturnsBasicAuthWithTokenOnly(t *testing.T) {
	c := &Credentials{RepoURL: "https://github.com/acme/app.git", Token: "token"}
	auth, err := c.Auth()
	if err != nil {
		t.Fatalf("Auth() error = %v", err)
	}
	if auth == nil {
		t.Fatal("Auth() should return non-nil when Token is set")
	}
}

func TestIsSSHRecognisesBothSpellings(t *testing.T) {
	ssh := []string{
		"ssh://git@github.com/dymurray/game-of-life.git",
		"ssh://git@git-ssh.example.com:443/acme/app.git",
		"git@github.com:dymurray/game-of-life.git",
	}
	for _, u := range ssh {
		if !(&Credentials{RepoURL: u}).IsSSH() {
			t.Errorf("IsSSH(%q) = false, want true", u)
		}
	}
	notSSH := []string{
		"https://github.com/acme/app.git",
		"http://git.example.com/acme/app.git",
	}
	for _, u := range notSSH {
		if (&Credentials{RepoURL: u}).IsSSH() {
			t.Errorf("IsSSH(%q) = true, want false", u)
		}
	}
}

// The fix: an SSH remote with a key gets go-git's public-key method, not
// the HTTP BasicAuth that the ssh transport rejects.
func TestAuthReturnsPublicKeysForSSHRemote(t *testing.T) {
	for _, url := range []string{
		"ssh://git@github.com/dymurray/game-of-life.git",
		"git@github.com:dymurray/game-of-life.git",
	} {
		t.Run(url, func(t *testing.T) {
			c := &Credentials{RepoURL: url, Username: "u", Token: "t",
				PrivateKey: testPrivateKey(t), IdentityName: "ssh"}

			got, err := c.Auth()
			if err != nil {
				t.Fatalf("Auth() error = %v", err)
			}
			if _, isBasic := got.(*http.BasicAuth); isBasic {
				t.Fatal("SSH remote must not get HTTP BasicAuth")
			}
			// The concrete type is wrapped to carry a dial timeout, so
			// assert on the contract the ssh transport uses instead.
			sshAuth, ok := got.(gitssh.AuthMethod)
			if !ok {
				t.Fatalf("Auth() = %T, which the ssh transport will reject", got)
			}
			cfg, err := sshAuth.ClientConfig()
			if err != nil {
				t.Fatalf("ClientConfig() error = %v", err)
			}
			if cfg.User != "git" {
				t.Errorf("ssh user = %q, want %q", cfg.User, "git")
			}
			if len(cfg.Auth) == 0 {
				t.Error("ClientConfig carries no auth methods")
			}
			if cfg.Timeout <= 0 {
				t.Error("ClientConfig has no dial timeout; a blackholed host would hang the run")
			}
			if cfg.HostKeyCallback == nil {
				t.Error("ClientConfig has no host key callback")
			}
		})
	}
}

func TestAuthRejectsSSHRemoteWithoutKey(t *testing.T) {
	c := &Credentials{RepoURL: "git@github.com:dymurray/game-of-life.git",
		Username: "u", Token: "t", IdentityName: "http-only"}

	_, err := c.Auth()
	if !errors.Is(err, ErrSSHKeyRequired) {
		t.Fatalf("Auth() error = %v, want ErrSSHKeyRequired", err)
	}
	// The operator has to be able to tell which Hub identity to fix.
	if !contains(err.Error(), "http-only") {
		t.Errorf("error %q should name the identity", err)
	}
}

func TestAuthRejectsUnparseableKey(t *testing.T) {
	c := &Credentials{RepoURL: "git@github.com:acme/app.git",
		PrivateKey: "not a key", IdentityName: "ssh"}

	_, err := c.Auth()
	if err == nil {
		t.Fatal("Auth() should fail on an unparseable private key")
	}
	if contains(err.Error(), "not a key") {
		t.Error("the error must not echo key material")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// testPrivateKey generates a throwaway ed25519 key in OpenSSH PEM form.
// Generated per-run rather than committed, so no key-shaped blob lives
// in the repository and nothing here can be mistaken for a real secret.
func testPrivateKey(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := cryptossh.MarshalPrivateKey(priv, "harness-test")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return string(pem.EncodeToMemory(block))
}
