package kai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agenticv1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// defaultHubBaseURL is the in-cluster address of the Tackle Hub service. Runs
// that reference an application reach Hub through this endpoint unless it is
// overridden with --hub-url.
const defaultHubBaseURL = "http://tackle-hub.konveyor-tackle.svc:8080"

// runContext holds the application/inventory context the CLI resolves into a
// run's environment. The controller is domain-agnostic and injects nothing, so
// APP_ID, the Hub endpoint, the target branch and any credential Secret are all
// expressed here as plain env / envFrom on the run spec. The user supplies the
// application ID (which they know from the inventory); everything else is
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
	f.StringVar(&rc.gitSecret, "git-secret", "",
		"Secret with git credentials to import via envFrom; off by default "+
			"(the harness fetches source-control credentials from Hub)")
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
// No credential Secret is wired implicitly: the harness fetches source-control
// credentials from Hub and does not consume GH_TOKEN, and a stale required
// envFrom reference would keep the Sandbox pod from starting. A Secret is only
// imported when the user asks for one via --git-secret or --env-from.
func (rc *runContext) build() ([]corev1.EnvVar, []corev1.EnvFromSource, error) {
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

	// Import credential Secrets only when explicitly requested.
	var secrets []string
	if gitSecret := strings.TrimSpace(rc.gitSecret); gitSecret != "" {
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

// createRun persists any run-scoped credentials and then creates the run. The
// Hub PAT is never written into the run spec: when a token is resolved it is
// stored in a dedicated, run-owned Opaque Secret and injected as HUB_TOKEN via
// ValueFrom.SecretKeyRef. The Secret is created before the run (so a failure
// aborts without a dangling run) and, once the run exists, is adopted by it so
// Kubernetes garbage-collects the Secret when the run is deleted. Access to the
// run and the Secret is still governed by Kubernetes RBAC. run must be a
// *AgentRun or *AgentWorkflowRun.
func (rc *runContext) createRun(ctx context.Context, cl client.Client, run client.Object) error {
	env, envFrom, err := rc.build()
	if err != nil {
		return err
	}

	// Pull any literal HUB_TOKEN out of the env (last value wins, matching
	// Kubernetes env semantics) so the PAT goes into a Secret rather than the run
	// spec. This covers both the app-scoped token build() injects and an explicit
	// --env HUB_TOKEN=... supplied without --app.
	token := ""
	filtered := env[:0]
	for _, e := range env {
		if e.Name == hubTokenEnvVar && e.ValueFrom == nil {
			token = strings.TrimSpace(e.Value)
			continue
		}
		filtered = append(filtered, e)
	}
	env = filtered

	var secret *corev1.Secret
	if token != "" {
		secret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "kai-hub-token-", Namespace: run.GetNamespace()},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{hubTokenEnvVar: []byte(token)},
		}
		if err := cl.Create(ctx, secret); err != nil {
			return fmt.Errorf("failed to create Hub token Secret: %w", err)
		}
		env = append(env, corev1.EnvVar{Name: hubTokenEnvVar, ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secret.Name},
				Key:                  hubTokenEnvVar,
			},
		}})
	}

	var kind string
	switch r := run.(type) {
	case *agenticv1alpha1.AgentRun:
		kind = kindAgentRun
		r.Spec.Env, r.Spec.EnvFrom = env, envFrom
	case *agenticv1alpha1.AgentWorkflowRun:
		kind = kindAgentWorkflowRun
		r.Spec.Env, r.Spec.EnvFrom = env, envFrom
	default:
		return fmt.Errorf("unsupported run type %T", run)
	}

	if err := cl.Create(ctx, run); err != nil {
		createErr := fmt.Errorf("failed to create %s: %w", kind, err)
		if secret != nil {
			// Roll back the Secret we created. Use an uncancellable context so a
			// cancelled command still cleans up, and a UID precondition so a
			// replacement Secret can never be deleted by this attempt.
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			uid := secret.UID
			delErr := cl.Delete(cleanupCtx, secret, &client.DeleteOptions{
				Preconditions: &metav1.Preconditions{UID: &uid},
			})
			if delErr != nil && !apierrors.IsNotFound(delErr) {
				return errors.Join(createErr,
					fmt.Errorf("failed to clean up Hub token Secret %q: %w", secret.Name, delErr))
			}
		}
		return createErr
	}

	if secret != nil {
		// Adopt the Secret so it survives for the run's (and all workflow stages')
		// lifetime and is garbage-collected when the run is deleted.
		secret.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: agenticv1alpha1.GroupVersion.String(), Kind: kind,
			Name: run.GetName(), UID: run.GetUID(),
		}}
		if err := cl.Update(ctx, secret); err != nil {
			// The run already exists and may be executing; never remove its
			// credential. Surface both names so the Secret can be cleaned up by hand.
			return fmt.Errorf("%s %q created, but failed to attach ownership to Hub token Secret %q "+
				"(delete it manually after deleting the run): %w", kind, run.GetName(), secret.Name, err)
		}
	}
	return nil
}
