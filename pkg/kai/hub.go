package kai

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/konveyor/tackle2-hub/shared/api"
	"github.com/konveyor/tackle2-hub/shared/binding"
	"github.com/konveyor/tackle2-hub/shared/binding/auth"
	"github.com/spf13/cobra"
)

const (
	// defaultTokenLifespan is the lifespan, in hours, of the personal access
	// token minted by 'hub login'. 720h is 30 days.
	defaultTokenLifespan = 720
	// defaultOIDCClientID is the OIDC client used for the device flow. Tackle
	// Hub registers "kantra" with the device_code grant (the "web-ui" client is
	// not permitted it), so it is the client CLIs authenticate as.
	defaultOIDCClientID = "kantra"
	// hubTokenEnvVar is the environment variable holding an existing Hub
	// personal access token; when set, 'hub login' stores it directly.
	hubTokenEnvVar = "HUB_TOKEN"
)

func newHubCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hub",
		Short: "Authenticate with Tackle Hub",
	}
	cmd.AddCommand(newHubLoginCommand())
	cmd.AddCommand(newHubLogoutCommand())
	return cmd
}

func newHubLoginCommand() *cobra.Command {
	var (
		hubURL     string
		issuerURL  string
		clientID   string
		lifespan   int
		insecure   bool
		tokenStdin bool
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to Tackle Hub and save a token for runs",
		Long: "Authenticate to Tackle Hub and save a token that subsequent " +
			"'agent run' / 'workflow run' invocations targeting an application " +
			"(--app) inject as HUB_TOKEN.\n\n" +
			"By default an interactive terminal uses the OIDC device flow: a URL " +
			"and code are printed for you to approve in a browser, after which a " +
			"personal access token (PAT) is minted. If your Hub's device flow " +
			"isn't available, create a PAT in the Hub UI and provide it with " +
			"--token-stdin, or set the HUB_TOKEN environment variable; either is " +
			"validated and saved as-is.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHubLogin(hubURL, issuerURL, clientID, lifespan, insecure, tokenStdin)
		},
	}
	cmd.Flags().StringVar(&hubURL, "hub-url", "", "Tackle Hub base URL (e.g. the OpenShift Route); required")
	cmd.Flags().StringVar(&issuerURL, "issuer-url", "", "OIDC issuer URL (default: derived from --hub-url)")
	cmd.Flags().StringVar(&clientID, "client-id", defaultOIDCClientID, "OIDC client ID")
	cmd.Flags().IntVar(&lifespan, "lifespan", defaultTokenLifespan, "minted token lifespan in hours (device flow only)")
	cmd.Flags().BoolVar(&insecure, "insecure", false, "skip TLS certificate verification")
	cmd.Flags().BoolVar(&tokenStdin, "token-stdin", false,
		"read an existing Hub personal access token from stdin instead of using the device flow")
	_ = cmd.MarkFlagRequired("hub-url")
	return cmd
}

// deriveIssuerURL returns the OIDC issuer for a Hub base URL. The issuer is
// served at the route root ("<host>/oidc"), not under the Hub's "/hub" path, so
// a trailing "/hub" is trimmed before appending "/oidc".
func deriveIssuerURL(hubURL string) string {
	base := strings.TrimSuffix(strings.TrimRight(hubURL, "/"), "/hub")
	return base + "/oidc"
}

func runHubLogin(hubURL, issuerURL, clientID string, lifespan int, insecure, tokenStdin bool) error {
	hubURL = strings.TrimSpace(hubURL)
	if hubURL == "" {
		return fmt.Errorf("--hub-url is required")
	}
	// Require https so credentials are never sent in cleartext; --insecure only
	// relaxes TLS certificate verification (for self-signed Routes), not the scheme.
	if u, err := url.Parse(hubURL); err != nil {
		return fmt.Errorf("invalid --hub-url %q: %w", hubURL, err)
	} else if u.Scheme != schemeHTTPS {
		return fmt.Errorf("--hub-url must use https (got %q); use --insecure for a self-signed certificate", hubURL)
	}
	if strings.TrimSpace(issuerURL) == "" {
		issuerURL = deriveIssuerURL(hubURL)
	}
	if strings.TrimSpace(clientID) == "" {
		clientID = defaultOIDCClientID
	}

	// Build a Hub client. Its transport is shared with the OIDC authenticator
	// so --insecure applies to both the device-flow calls and the token mint.
	rc := binding.New(hubURL)
	rc.Client.SetRetry(1)
	tr := rc.Client.Transport()
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in via --insecure
	}

	token, err := resolveHubLoginToken(rc, tr, issuerURL, clientID, lifespan, tokenStdin)
	if err != nil {
		return err
	}

	// Validate the token against the Hub before persisting it, so a typo or an
	// unreachable Hub is reported now rather than on the first run.
	rc.Client.Use(auth.NewBearer(token))
	if _, err := rc.User.List(); err != nil {
		return fmt.Errorf("token validation failed: %w", err)
	}

	dir, err := hubConfigDir()
	if err != nil {
		return err
	}
	if err := saveHubToken(dir, hubCredentials{HubURL: hubURL, Token: token}); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stdout, "\nlogged in to %s; token saved to %s\n",
		hubURL, fmt.Sprintf("%s/%s", dir, hubTokenFile))
	return nil
}

// resolveHubLoginToken obtains a Hub personal access token, in priority order:
// the HUB_TOKEN environment variable, an existing token read from stdin
// (--token-stdin or a non-interactive shell), else the OIDC device flow which
// mints a fresh token. It never echoes the token.
func resolveHubLoginToken(
	rc *binding.RichClient, tr *http.Transport, issuerURL, clientID string, lifespan int, tokenStdin bool,
) (string, error) {
	if t := strings.TrimSpace(os.Getenv(hubTokenEnvVar)); t != "" {
		_, _ = fmt.Fprintf(os.Stdout, "using %s from the environment\n", hubTokenEnvVar)
		return t, nil
	}
	if tokenStdin || !isInteractive() {
		return readHubTokenFromStdin(tokenStdin)
	}
	return deviceFlowToken(rc, tr, issuerURL, clientID, lifespan)
}

// deviceFlowToken runs the OIDC device flow (prints a verification URL and user
// code, polls until approved) and mints a durable personal access token.
func deviceFlowToken(
	rc *binding.RichClient, tr *http.Transport, issuerURL, clientID string, lifespan int,
) (string, error) {
	oidc := auth.NewOIDC(issuerURL, clientID)
	oidc.SetTransport(tr)
	_, _ = fmt.Fprintf(os.Stdout, "Logging in via %s...\n", issuerURL)
	if err := oidc.Login(); err != nil {
		return "", fmt.Errorf(
			"device-flow login failed (create a PAT in the Hub UI and use --token-stdin, or set %s): %w",
			hubTokenEnvVar, err)
	}
	rc.Client.Use(auth.NewBearer(oidc.Token()))
	pat := &api.PAT{Lifespan: lifespan, Description: "kubectl-kai"}
	if err := rc.Token.Create(pat); err != nil {
		return "", fmt.Errorf("failed to mint token: %w", err)
	}
	return pat.Token, nil
}

// readHubTokenFromStdin reads an existing PAT. On an interactive terminal it
// shows a masked prompt; otherwise it reads a single line piped in.
func readHubTokenFromStdin(explicit bool) (string, error) {
	if isInteractive() {
		var token string
		if err := runForm(passwordField("Hub personal access token", &token, requiredValidator("token"))); err != nil {
			return "", err
		}
		token = strings.TrimSpace(token)
		if token == "" {
			return "", fmt.Errorf("a Hub personal access token is required")
		}
		return token, nil
	}
	if !explicit {
		return "", fmt.Errorf(
			"no interactive terminal: set %s or pass --token-stdin with a token piped to stdin", hubTokenEnvVar)
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("failed to read token from stdin: %w", err)
	}
	token := strings.TrimSpace(line)
	if token == "" {
		return "", fmt.Errorf("a Hub personal access token is required on stdin")
	}
	return token, nil
}

func newHubLogoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the saved Tackle Hub token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := hubConfigDir()
			if err != nil {
				return err
			}
			if err := deleteHubToken(dir); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(os.Stdout, "logged out; saved Hub token removed")
			return nil
		},
	}
}
