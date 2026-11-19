package git

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	cryptossh "golang.org/x/crypto/ssh"

	"github.com/konveyor/migration-harness/internal/logging"
)

// ErrSSHKeyRequired marks an SSH remote the harness was given no private
// key for. The harness clones in-process with go-git, which cannot fall
// back to the system git or to an SSH agent the sandbox does not run, so
// a key on the Hub identity is the only way in. Without it the clone
// would otherwise die inside the transport with "invalid auth method",
// naming neither SSH nor the identity.
var ErrSSHKeyRequired = errors.New("ssh remote requires an identity with a private key")

// knownHostsEnv names the known_hosts file(s) to verify servers against,
// as an OS-separated list. When it is set, its files are authoritative:
// a path that does not exist is an error, because an operator who
// configured verification must not silently get none.
//
// When it is unset the locations OpenSSH reads are used if they exist,
// and verification is skipped with a warning if they do not. The agent
// sandbox is a fresh container that ships no known_hosts, so requiring
// one there would mean no SSH remote could ever be cloned.
const knownHostsEnv = "HARNESS_GIT_SSH_KNOWN_HOSTS"

// sshDialTimeout bounds the TCP connect. go-git leaves
// ssh.ClientConfig.Timeout at zero and dials on context.Background(), so
// the context passed to Clone cannot interrupt it: a host that drops the
// SYN is otherwise bounded only by the kernel's SYN-retry limit. A var,
// not a const, so a test can shorten it.
//
// This covers the dial only. x/crypto documents Timeout as "the maximum
// amount of time for the TCP connection to establish" and go-git hands
// the connection to ssh.NewClientConn without a deadline, so the
// handshake is bounded separately — see probeSSHSetup.
var sshDialTimeout = 30 * time.Second

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

	cb, algos, err := hostKeyPolicy(hostWithPort(ep))
	if err != nil {
		return nil, err
	}
	keys.HostKeyCallback = cb
	return &sshAuthMethod{AuthMethod: keys, timeout: sshDialTimeout, hostKeyAlgorithms: algos}, nil
}

// sshAuthMethod carries the settings go-git will not derive for itself
// once a host key callback is set. It is the only hook available: the
// transport's own ClientConfig is applied through overrideConfig, which
// copies every field and would wipe the user, signer and callback.
type sshAuthMethod struct {
	gitssh.AuthMethod
	timeout time.Duration
	// hostKeyAlgorithms pins the signature algorithms to the key types
	// known_hosts actually holds for this host. go-git derives these
	// itself only when it installs its own callback; with ours it
	// explicitly leaves them to the caller, and an empty list means
	// x/crypto offers its default preference order instead. A server
	// offering several key types would then be matched on whichever
	// x/crypto ranks first, which a known_hosts pinning only one of the
	// others rejects as an unknown key.
	hostKeyAlgorithms []string
}

func (a *sshAuthMethod) ClientConfig() (*cryptossh.ClientConfig, error) {
	cfg, err := a.AuthMethod.ClientConfig()
	if err != nil {
		return nil, err
	}
	cfg.Timeout = a.timeout
	if len(a.hostKeyAlgorithms) > 0 {
		cfg.HostKeyAlgorithms = a.hostKeyAlgorithms
	}
	return cfg, nil
}

// hostWithPort is the address known_hosts is keyed by, and the one
// go-git dials. An omitted port is SSH's default.
func hostWithPort(ep *transport.Endpoint) string {
	port := ep.Port
	if port <= 0 {
		port = 22
	}
	return net.JoinHostPort(ep.Host, strconv.Itoa(port))
}

// hostKeyPolicy returns the host key callback and the signature
// algorithms to advertise for hostWithPort.
//
// Parsing is delegated to go-git's NewKnownHostsDb, which reads every
// file given rather than only the first, so a host trusted in the system
// file is still trusted when the user file exists too.
func hostKeyPolicy(hostWithPort string) (cryptossh.HostKeyCallback, []string, error) {
	files, err := knownHostsFiles()
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		logging.Warn("no known_hosts found - skipping SSH host key verification; "+
			"set %s to a known_hosts file to verify the server", knownHostsEnv)
		return cryptossh.InsecureIgnoreHostKey(), nil, nil
	}

	db, err := gitssh.NewKnownHostsDb(files...)
	if err != nil {
		return nil, nil, fmt.Errorf("read known_hosts (%s): %w", strings.Join(files, ", "), err)
	}
	logging.Info("ssh host key verification against %s", strings.Join(files, ", "))
	return db.HostKeyCallback(), db.HostKeyAlgorithms(hostWithPort), nil
}

// knownHostsFiles returns the trust files that exist.
//
// A file that exists but cannot be read is an error, not an absence:
// treating a permission failure as "no known_hosts" would drop the run
// to no verification at all, which is the opposite of what a deployment
// that provisioned the file asked for.
func knownHostsFiles() ([]string, error) {
	configured := strings.TrimSpace(os.Getenv(knownHostsEnv))
	candidates := defaultKnownHosts()
	if configured != "" {
		candidates = filepath.SplitList(configured)
	}

	var files []string
	for _, path := range candidates {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read known_hosts %s: %w", path, err)
		}
		files = append(files, path)
	}

	// An explicitly configured path that is not there is a
	// misconfiguration. Falling back to the unverified path would hand
	// the operator the one outcome they ruled out by setting it.
	if configured != "" && len(files) == 0 {
		return nil, fmt.Errorf("%s=%s: no such file", knownHostsEnv, configured)
	}
	return files, nil
}

// defaultKnownHosts are the locations OpenSSH reads, used when
// knownHostsEnv is unset so an image that ships known_hosts gets strict
// verification without further configuration. Resolved per call rather
// than at init so HOME is read when it matters.
func defaultKnownHosts() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return []string{"/etc/ssh/ssh_known_hosts"}
	}
	return []string{
		filepath.Join(home, ".ssh", "known_hosts"),
		"/etc/ssh/ssh_known_hosts",
	}
}
