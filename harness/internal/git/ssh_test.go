package git

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport"
)

// SSH remotes reach go-git's ssh transport, which accepts only its own
// AuthMethod. Before the harness learned to build one, every SSH clone
// died with transport.ErrInvalidAuthMethod — a message naming neither
// SSH nor the Hub identity that was missing a key. These tests pin the
// behaviour on both sides of that fix.
//
// They are offline: the no-key cases fail while building the auth
// method, and the key-bearing case points at a closed local port, so no
// name is resolved and no remote is contacted.

const sshRepoURL = "ssh://git@git-ssh.example.com:443/acme/app.git"

// scpStyleRepoURL is the other spelling of the same thing
// (git@host:path), which go-git parses into the same ssh transport.
const scpStyleRepoURL = "git@git-ssh.example.com:acme/app.git"

// closedPortSSHURL resolves nothing: 127.0.0.1:1 refuses immediately.
const closedPortSSHURL = "ssh://git@127.0.0.1:1/acme/app.git"

// An SSH remote whose identity carries no key now fails by name, and
// before the clone opens a socket.
func TestCloneSSHURLWithoutKeyNamesTheIdentity(t *testing.T) {
	for _, url := range []string{sshRepoURL, scpStyleRepoURL} {
		t.Run(url, func(t *testing.T) {
			cred := &Credentials{RepoURL: url, Username: "u", Token: "t", IdentityName: "http-only"}

			_, err := Clone(context.Background(), cred, filepath.Join(t.TempDir(), "repo"))
			if !errors.Is(err, ErrSSHKeyRequired) {
				t.Fatalf("Clone error = %v, want ErrSSHKeyRequired", err)
			}
			if errors.Is(err, transport.ErrInvalidAuthMethod) {
				t.Error("regression: SSH clone is back to reporting invalid auth method")
			}
		})
	}
}

// Anonymous SSH used to fail complaining about a missing SSH agent,
// which is not something an operator can act on. There is no anonymous
// SSH; the run needs an identity with a key.
func TestCloneSSHURLAnonymousNamesTheMissingKey(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	cred := &Credentials{RepoURL: sshRepoURL}

	_, err := Clone(context.Background(), cred, filepath.Join(t.TempDir(), "repo"))
	if !errors.Is(err, ErrSSHKeyRequired) {
		t.Fatalf("Clone error = %v, want ErrSSHKeyRequired", err)
	}
}

// With a key, the clone gets past auth-method selection and fails at the
// network instead — which is the whole point of the fix. Nothing here
// asserts a successful clone; that is what the live cluster run proves.
func TestCloneSSHURLWithKeyReachesTheNetwork(t *testing.T) {
	cred := &Credentials{RepoURL: closedPortSSHURL, PrivateKey: testPrivateKey(t), IdentityName: "ssh"}

	_, err := Clone(context.Background(), cred, filepath.Join(t.TempDir(), "repo"))
	if err == nil {
		t.Fatal("Clone against a closed port should fail")
	}
	if errors.Is(err, transport.ErrInvalidAuthMethod) {
		t.Fatalf("regression: auth method still rejected by the ssh transport: %v", err)
	}
	if errors.Is(err, ErrSSHKeyRequired) {
		t.Fatalf("a key was supplied but Clone reported it missing: %v", err)
	}
}

// GIT_SSH_COMMAND was the proposed workaround. It is an instruction to
// the git CLI, and the harness never runs the git CLI — go-git builds
// the SSH connection itself. Setting it changes nothing, which is why
// the fix had to be in the harness.
func TestGitSSHCommandIsIgnored(t *testing.T) {
	cred := &Credentials{RepoURL: closedPortSSHURL, PrivateKey: testPrivateKey(t), IdentityName: "ssh"}

	first, _ := Clone(context.Background(), cred, filepath.Join(t.TempDir(), "repo"))
	_ = first

	before := cloneErr(t, cred)
	t.Setenv("GIT_SSH_COMMAND",
		`ssh -i /tmp/id -o ProxyCommand='openssl s_client -quiet -connect %h:443 -servername %h'`)
	after := cloneErr(t, cred)

	if before != after {
		t.Errorf("GIT_SSH_COMMAND changed the outcome:\n before = %s\n after  = %s", before, after)
	}
}

func cloneErr(t *testing.T, cred *Credentials) string {
	t.Helper()
	_, err := Clone(context.Background(), cred, filepath.Join(t.TempDir(), "repo"))
	if err == nil {
		t.Fatal("expected Clone to fail")
	}
	return err.Error()
}

// A missing key makes the pre-flight inconclusive rather than a
// refusal: the run is killed by the clone, which runs first and now
// reports the real reason.
func TestCheckWriteAccessSSHWithoutKeyIsInconclusive(t *testing.T) {
	cred := &Credentials{RepoURL: sshRepoURL, Username: "u", Token: "t", IdentityName: "http-only"}

	err := CheckWriteAccess(context.Background(), cred)
	if !errors.Is(err, ErrWriteAccessUnknown) {
		t.Errorf("error = %v, want ErrWriteAccessUnknown", err)
	}
	if errors.Is(err, ErrNoWriteAccess) {
		t.Error("a missing key is not the server refusing a credential")
	}
}

// A host that drops the SYN must not hang the run. go-git dials on
// context.Background() with no deadline of its own, so without the
// timeout the harness installs, Clone never returns — which is exactly
// what a cluster with no outbound port 22 produces.
//
// 203.0.113.0/24 is TEST-NET-3 (RFC 5737): reserved for documentation
// and not routed, so the connection attempt is dropped rather than
// refused.
func TestCloneSSHURLTimesOutOnBlackholedHost(t *testing.T) {
	old := sshDialTimeout
	sshDialTimeout = 2 * time.Second
	t.Cleanup(func() { sshDialTimeout = old })

	cred := &Credentials{
		RepoURL:      "ssh://git@203.0.113.1:22/acme/app.git",
		PrivateKey:   testPrivateKey(t),
		IdentityName: "ssh",
	}

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := Clone(context.Background(), cred, filepath.Join(t.TempDir(), "repo"))
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Clone to a blackholed host should fail")
		}
		if elapsed := time.Since(start); elapsed > 20*time.Second {
			t.Errorf("Clone took %s; the dial timeout did not apply", elapsed)
		}
	case <-time.After(25 * time.Second):
		t.Fatal("Clone hung: the SSH dial timeout is not being applied")
	}
}
