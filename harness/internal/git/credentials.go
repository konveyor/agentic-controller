package git

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	cryptossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/konveyor/migration-harness/internal/logging"
)

// ErrSSHKeyRequired marks an SSH remote the harness was given no private
// key for. The harness clones in-process with go-git, which cannot fall
// back to the system git or to an SSH agent the sandbox does not run, so
// a key on the Hub identity is the only way in. Without it the clone
// would otherwise die inside the transport with "invalid auth method",
// naming neither SSH nor the identity.
var ErrSSHKeyRequired = errors.New("ssh remote requires an identity with a private key")

// sshDialTimeout bounds the TCP connect and SSH handshake. go-git
// leaves ssh.ClientConfig.Timeout at zero and dials on
// context.Background(), so the context passed to Clone cannot interrupt
// it: a host that drops the SYN — an egress policy with no outbound
// port 22, which is the common case in a cluster — hangs the run
// forever instead of failing. A var, not a const, so a test can shorten
// it.
var sshDialTimeout = 30 * time.Second

// knownHostsEnv points at a known_hosts file to verify the server
// against. When it is unset and no default known_hosts exists, host key
// verification is skipped with a warning — the sandbox is a fresh
// container with no known_hosts, so requiring one would mean no SSH
// remote could ever be cloned.
const knownHostsEnv = "HARNESS_GIT_SSH_KNOWN_HOSTS"

// defaultKnownHosts are the locations OpenSSH reads, checked when
// knownHostsEnv is unset so an image that ships known_hosts gets strict
// verification without further configuration.
var defaultKnownHosts = []string{
	os.ExpandEnv("$HOME/.ssh/known_hosts"),
	"/etc/ssh/ssh_known_hosts",
}

type Credentials struct {
	Username string
	Token    string
	RepoURL  string
	Branch   string
	// PrivateKey is the PEM private key from the Hub identity (its Key
	// field), used for SSH remotes. Empty for HTTP identities.
	PrivateKey string
	// KeyPassphrase decrypts PrivateKey when it is encrypted. Hub stores
	// it in the identity's Password field, which for an HTTP identity is
	// instead the token — the two never apply to the same remote.
	KeyPassphrase string
	// IdentityName is the Hub identity these credentials came from, empty
	// when none resolved and the run is anonymous. Carried so a failed
	// write-access pre-flight can name the credential that cannot push
	// instead of only saying that pushing failed (issue #247). Never the
	// secret itself — only the identity's display name.
	IdentityName string
}

// IsSSH reports whether RepoURL is an SSH remote, in either spelling:
// ssh://git@host/org/repo.git or the scp-like git@host:org/repo.git.
// An unparseable URL is not claimed as SSH; the transport will reject it
// soon enough with a better message than this function could give.
func (c *Credentials) IsSSH() bool {
	ep, err := transport.NewEndpoint(c.RepoURL)
	return err == nil && ep.Protocol == "ssh"
}

// Auth selects the authentication method for the remote's transport.
//
// The scheme decides, not the identity: go-git's transports each accept
// only their own AuthMethod, so handing an SSH remote the HTTP BasicAuth
// built from a username and token fails in the transport with "invalid
// auth method" rather than anywhere a reader would look. SSH remotes get
// a public-key method built from the identity's private key; everything
// else gets HTTP Basic.
//
// It returns a nil AuthMethod — a truly nil interface, not a typed-nil
// *http.BasicAuth — when no identity was resolved for an HTTP remote:
// go-git treats any non-nil AuthMethod as an auth attempt, which
// non-HTTP transports (git://) reject and the HTTP transport would
// deref. An SSH remote with no key is an error instead, because there is
// no anonymous SSH.
func (c *Credentials) Auth() (transport.AuthMethod, error) {
	ep, err := transport.NewEndpoint(c.RepoURL)
	if err != nil {
		return nil, fmt.Errorf("parse repository URL %q: %w", c.RepoURL, err)
	}
	if ep.Protocol == "ssh" {
		return c.sshAuth(ep)
	}
	if c.Username == "" && c.Token == "" {
		return nil, nil
	}
	return &http.BasicAuth{
		Username: c.Username,
		Password: c.Token,
	}, nil
}

// sshAuth builds public-key auth for an SSH remote.
func (c *Credentials) sshAuth(ep *transport.Endpoint) (transport.AuthMethod, error) {
	if c.PrivateKey == "" {
		if c.IdentityName == "" {
			return nil, fmt.Errorf("%w: %s resolved no identity", ErrSSHKeyRequired, c.RepoURL)
		}
		return nil, fmt.Errorf("%w: identity %q for %s has no key",
			ErrSSHKeyRequired, c.IdentityName, c.RepoURL)
	}

	// The user travels in the URL for SSH remotes (git@github.com), not
	// in the identity. "git" is the near-universal default when a URL
	// omits it.
	user := ep.User
	if user == "" {
		user = "git"
	}

	keys, err := gitssh.NewPublicKeys(user, []byte(c.PrivateKey), c.KeyPassphrase)
	if err != nil {
		// The key material must not reach the message; go-git's error
		// here names the parse failure, not the bytes.
		return nil, fmt.Errorf("parse private key from identity %q: %w", c.IdentityName, err)
	}

	cb, err := hostKeyCallback()
	if err != nil {
		return nil, err
	}
	keys.HostKeyCallback = cb
	return &timeoutAuth{AuthMethod: keys, timeout: sshDialTimeout}, nil
}

// timeoutAuth is an ssh AuthMethod that bounds the dial. go-git reads
// the deadline off the ClientConfig the auth method hands it, which is
// the only hook it offers: the transport's own config would be applied
// through overrideConfig, which copies every field and would therefore
// wipe the user, signer and host key callback set above.
type timeoutAuth struct {
	gitssh.AuthMethod
	timeout time.Duration
}

func (a *timeoutAuth) ClientConfig() (*cryptossh.ClientConfig, error) {
	cfg, err := a.AuthMethod.ClientConfig()
	if err != nil {
		return nil, err
	}
	cfg.Timeout = a.timeout
	return cfg, nil
}

// hostKeyCallback picks the host key policy. A known_hosts file named by
// HARNESS_GIT_SSH_KNOWN_HOSTS, or found where OpenSSH would look, is
// verified strictly. With neither, verification is skipped and the run
// logs that it did: the agent sandbox is a fresh container that ships no
// known_hosts, so the alternative is that SSH remotes never clone.
func hostKeyCallback() (cryptossh.HostKeyCallback, error) {
	if path := strings.TrimSpace(os.Getenv(knownHostsEnv)); path != "" {
		cb, err := knownhosts.New(path)
		if err != nil {
			return nil, fmt.Errorf("read %s=%s: %w", knownHostsEnv, path, err)
		}
		logging.Info("ssh host key verification against %s", path)
		return cb, nil
	}
	for _, path := range defaultKnownHosts {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		cb, err := knownhosts.New(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		logging.Info("ssh host key verification against %s", path)
		return cb, nil
	}
	logging.Warn("no known_hosts found - skipping SSH host key verification; "+
		"set %s to a known_hosts file to verify the server", knownHostsEnv)
	return cryptossh.InsecureIgnoreHostKey(), nil
}
