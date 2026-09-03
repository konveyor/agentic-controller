package kai

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
)

// defaultHubBaseURL is the in-cluster address of the Tackle Hub service. Runs
// that reference an application reach Hub through this endpoint unless it is
// overridden with --hub-url.
const defaultHubBaseURL = "http://tackle-hub.konveyor-tackle.svc:8080"

// defaultGitSecret is the Secret carrying git push credentials (GH_TOKEN). It is
// wired via envFrom onto application-scoped runs unless overridden. The Agent CR
// cannot carry envFrom, so this must be set on every run that pushes.
const defaultGitSecret = "github-credentials"

// runContext holds the application/inventory context the CLI resolves into a
// run's environment. The controller is domain-agnostic and injects nothing, so
// APP_ID, the Hub endpoint, the target branch and the git-credentials Secret are
// all expressed here as plain env / envFrom on the run spec. The user supplies
// the application ID (which they know from the inventory); everything else is
// defaulted or passed through so a single --app is usually enough.
type runContext struct {
	appID        string
	hubBaseURL   string
	targetBranch string
	gitSecret    string
	env          []string
	envFrom      []string
	// hubToken is the Hub credential injected as HUB_TOKEN. It is populated
	// from the --hub-token flag, else from the token saved by 'hub login'.
	hubToken string
}

// addFlags registers the application-context flags on a run command.
func (rc *runContext) addFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&rc.appID, "app", "", "application ID from the inventory; sets APP_ID and the Hub context on the run")
	f.StringVar(&rc.hubBaseURL, "hub-url", defaultHubBaseURL,
		"in-cluster Hub address the run pod uses as HUB_BASE_URL (applied with --app); "+
			"distinct from the external route passed to 'hub login'")
	f.StringVar(&rc.targetBranch, "target-branch", "", "TARGET_BRANCH for the run")
	f.StringVar(&rc.gitSecret, "git-secret", defaultGitSecret,
		"Secret providing git credentials (GH_TOKEN) via envFrom; empty to skip")
	f.StringVar(&rc.hubToken, "hub-token", "",
		"HUB_TOKEN for the run (overrides the token saved by 'hub login'; applied with --app)")
	f.StringArrayVar(&rc.env, "env", nil, "additional environment variable as NAME=VALUE (repeatable)")
	f.StringArrayVar(&rc.envFrom, "env-from", nil, "additional Secret to import as envFrom (repeatable)")
}

// resolveHubToken fills hubToken from the token saved by 'hub login' when it was
// not supplied via --hub-token and the run targets an application. A missing
// saved token is not an error: the run simply carries no HUB_TOKEN.
//
// Only the token is restored, not the saved login URL: 'hub login' targets the
// external Hub route (reachable from a laptop), whereas the run's HUB_BASE_URL
// must be the in-cluster service address the sandbox pod can reach (--hub-url,
// default defaultHubBaseURL). The minted PAT is portable across both.
func (rc *runContext) resolveHubToken() error {
	if rc.hubToken != "" || rc.appID == "" {
		return nil
	}
	dir, err := hubConfigDir()
	if err != nil {
		return err
	}
	c, err := loadHubToken(dir)
	if err != nil {
		return err
	}
	rc.hubToken = c.Token
	return nil
}

// build resolves the flags into env vars and envFrom sources for a run spec.
// gitSecretChanged reports whether the user set --git-secret explicitly, so the
// default git Secret is only wired onto application-scoped runs and a plain run
// the user never opted into git for stays clean.
func (rc *runContext) build(gitSecretChanged bool) ([]corev1.EnvVar, []corev1.EnvFromSource, error) {
	var env []corev1.EnvVar
	if rc.appID != "" {
		env = append(env, corev1.EnvVar{Name: "APP_ID", Value: rc.appID})
		if strings.TrimSpace(rc.hubBaseURL) != "" {
			env = append(env, corev1.EnvVar{Name: "HUB_BASE_URL", Value: strings.TrimSpace(rc.hubBaseURL)})
		}
		if strings.TrimSpace(rc.hubToken) != "" {
			env = append(env, corev1.EnvVar{Name: hubTokenEnvVar, Value: strings.TrimSpace(rc.hubToken)})
		}
	}
	if tb := strings.TrimSpace(rc.targetBranch); tb != "" {
		env = append(env, corev1.EnvVar{Name: "TARGET_BRANCH", Value: tb})
	}
	for _, e := range rc.env {
		name, value, ok := strings.Cut(e, "=")
		if !ok {
			return nil, nil, fmt.Errorf("invalid --env %q, expected NAME=VALUE", e)
		}
		env = append(env, corev1.EnvVar{Name: strings.TrimSpace(name), Value: value})
	}

	// Wire the git-credentials Secret. Only default it onto application-scoped
	// runs; if the user set --git-secret explicitly, honor it regardless.
	var secrets []string
	if gitSecret := strings.TrimSpace(rc.gitSecret); gitSecret != "" && (gitSecretChanged || rc.appID != "") {
		secrets = append(secrets, gitSecret)
	}
	secrets = append(secrets, rc.envFrom...)

	var envFrom []corev1.EnvFromSource
	for _, s := range secrets {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		envFrom = append(envFrom, corev1.EnvFromSource{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: s},
			},
		})
	}
	return env, envFrom, nil
}
