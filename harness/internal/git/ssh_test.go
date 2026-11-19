package git

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	cryptossh "golang.org/x/crypto/ssh"
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
	oldDial, oldSetup := sshDialTimeout, sshSetupTimeout
	sshDialTimeout, sshSetupTimeout = 2*time.Second, 2*time.Second
	t.Cleanup(func() { sshDialTimeout, sshSetupTimeout = oldDial, oldSetup })

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

// A server that accepts TCP and then says nothing must not stall the
// run. go-git's ssh.ClientConfig.Timeout covers the dial only, and it
// calls ssh.NewClientConn without a deadline, so nothing below the
// harness bounds the handshake.
func TestCloneSSHURLBoundsStalledHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	// Accept and hold the connection open without ever sending the SSH
	// identification string.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	oldSetup := sshSetupTimeout
	sshSetupTimeout = 2 * time.Second
	t.Cleanup(func() { sshSetupTimeout = oldSetup })

	cred := &Credentials{
		RepoURL:      "ssh://git@" + ln.Addr().String() + "/acme/app.git",
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
			t.Fatal("Clone against a silent server should fail")
		}
		if elapsed := time.Since(start); elapsed > 20*time.Second {
			t.Errorf("Clone took %s; the handshake was not bounded", elapsed)
		}
	case <-time.After(25 * time.Second):
		t.Fatal("Clone hung: the SSH handshake is not bounded")
	}
}

// known_hosts pins a key type per host. go-git derives the matching
// signature algorithms only when it installs its own callback; with
// ours it leaves HostKeyAlgorithms empty, and x/crypto's default order
// would then negotiate whichever type it ranks first. A host pinned
// with any other type is rejected as unknown.
func TestAuthPinsHostKeyAlgorithmsFromKnownHosts(t *testing.T) {
	kh := writeKnownHosts(t, map[string]string{"github.com:22": "ssh-ed25519"})
	t.Setenv(knownHostsEnv, kh)

	cred := &Credentials{
		RepoURL:      "git@github.com:acme/app.git",
		PrivateKey:   testPrivateKey(t),
		IdentityName: "ssh",
	}
	auth, err := cred.Auth()
	if err != nil {
		t.Fatalf("Auth() error = %v", err)
	}
	cfg, err := auth.(gitssh.AuthMethod).ClientConfig()
	if err != nil {
		t.Fatalf("ClientConfig() error = %v", err)
	}
	if len(cfg.HostKeyAlgorithms) == 0 {
		t.Fatal("HostKeyAlgorithms is empty; a pinned non-default key type would be rejected")
	}
	for _, a := range cfg.HostKeyAlgorithms {
		if strings.Contains(a, "ed25519") {
			return
		}
	}
	t.Errorf("HostKeyAlgorithms = %v, want the pinned ed25519 type", cfg.HostKeyAlgorithms)
}

// Every trust file is read, not just the first that exists: a host
// trusted only in the second file must still verify.
func TestKnownHostsReadsEveryConfiguredFile(t *testing.T) {
	first := writeKnownHosts(t, map[string]string{"hosta.example.com:22": "ssh-ed25519"})
	second := writeKnownHosts(t, map[string]string{"hostb.example.com:22": "ssh-ed25519"})
	t.Setenv(knownHostsEnv, first+string(filepath.ListSeparator)+second)

	_, algos, err := hostKeyPolicy("hostb.example.com:22")
	if err != nil {
		t.Fatalf("hostKeyPolicy() error = %v", err)
	}
	if len(algos) == 0 {
		t.Error("host from the second trust file is not trusted; only the first file was read")
	}
}

// A trust file that exists but cannot be read is a misconfiguration.
// Treating the error as absence would silently drop the run to no host
// key verification at all.
func TestKnownHostsUnreadableFileIsAnErrorNotAbsence(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(locked, "known_hosts")
	if err := os.WriteFile(path, []byte("# empty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	t.Setenv(knownHostsEnv, path)
	if _, err := knownHostsFiles(); err == nil {
		t.Fatal("an unreadable known_hosts must be an error, not treated as absent")
	}
}

// An explicitly configured path that does not exist must fail rather
// than fall back: the operator who set it ruled that outcome out.
func TestKnownHostsConfiguredButMissingIsAnError(t *testing.T) {
	t.Setenv(knownHostsEnv, filepath.Join(t.TempDir(), "absent"))
	if _, err := knownHostsFiles(); err == nil {
		t.Fatal("a configured but missing known_hosts must be an error")
	}
}

// writeKnownHosts creates a known_hosts file pinning a freshly
// generated key of the named type for each host.
func writeKnownHosts(t *testing.T, hosts map[string]string) string {
	t.Helper()
	var b strings.Builder
	for hostPort := range hosts {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		sshPub, err := cryptossh.NewPublicKey(pub)
		if err != nil {
			t.Fatal(err)
		}
		host, port, err := net.SplitHostPort(hostPort)
		if err != nil {
			t.Fatal(err)
		}
		pattern := host
		if port != "22" {
			pattern = "[" + host + "]:" + port
		}
		b.WriteString(pattern + " " + string(cryptossh.MarshalAuthorizedKey(sshPub)))
	}
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
