/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"net/url"
	"slices"
	"strconv"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

const (
	// gatewayCredentialSecretIndexField indexes Gateways by the Secret their
	// credentialRef names, so a Secret event can be mapped back to the
	// Gateways that depend on it.
	gatewayCredentialSecretIndexField = ".spec.credentialRef.secretName"

	// verificationJobPrefix is the prefix for verification Job names.
	verificationJobPrefix = "gw-verify-"

	// DefaultVerificationImage is the default image used for Gateway
	// verification when no override is configured. In production, the
	// controller should use the agentic-controller-agent image from
	// this repository.
	DefaultVerificationImage = "quay.io/konveyor/agentic-controller-agent:latest"

	// verificationHTTPCodePattern requires a 2xx status from the probe.
	verificationHTTPCodePattern = "^2"

	// anthropicAPIVersion is sent as the anthropic-version header when
	// probing native Anthropic endpoints, which reject Authorization: Bearer.
	anthropicAPIVersion = "2023-06-01"

	// endpointModelsProbe is the models endpoint the connectivity probe
	// requests. Only providers with an OpenAI-compatible surface get this
	// probe; see providerProbeKind for the rest.
	endpointModelsProbe = "$LLM_ENDPOINT/v1/models"

	// endpointBedrockConverseProbe is the bedrock-runtime operation the
	// SigV4 probe calls. ConverseStream rather than Converse on purpose:
	// goose streams, so this is the operation a run actually performs and
	// the one an IAM policy or SCP has to allow
	// (bedrock:InvokeModelWithResponseStream). Both the endpoint and the
	// model id travel as env vars, so nothing from the spec is interpolated
	// into the command string.
	endpointBedrockConverseProbe = "$LLM_ENDPOINT/model/$LLM_MODEL/converse-stream"

	// bedrockSigningService is the service name in the SigV4 credential
	// scope for bedrock-runtime. The region comes from the credential
	// Secret's AWS_REGION, which is what the run signs with too.
	bedrockSigningService = "bedrock"

	// bedrockProbeBody is the smallest ConverseStream request that still
	// exercises the model: one short user turn, capped at a single output
	// token. A verification therefore costs a handful of tokens, and only
	// on a spec change or a credential rotation - not on every reconcile.
	bedrockProbeBody = `{"messages":[{"role":"user","content":[{"text":"ping"}]}],` +
		`"inferenceConfig":{"maxTokens":1}}`

	// AWS credential Secret keys. These are the names the AWS SDK itself
	// reads, which is why a Bedrock Gateway's Secret uses them and why
	// envFrom can hand them straight to curl.
	awsAccessKeyIDEnv     = "AWS_ACCESS_KEY_ID"
	awsSecretAccessKeyEnv = "AWS_SECRET_ACCESS_KEY"
	awsRegionEnv          = "AWS_REGION"

	// verificationDetailLimit bounds the provider error text a probe copies
	// into its termination message, which the kubelet caps at 4KiB.
	verificationDetailLimit = 300

	// diagDetailPrefix introduces the free-text tail of a probe diagnostic
	// (the provider's own error body). It must come last on the line - see
	// diagDetail.
	diagDetailPrefix = " detail="

	// Provider identifiers in normalized form (see normalizeProvider), kept
	// in sync with the harness providerEnv switch.
	providerAnthropic  = "anthropic"
	providerOpenAI     = "openai"
	providerXAI        = "xai"
	providerGCPVertex  = "gcp_vertex_ai"
	providerAWSBedrock = "aws_bedrock"

	// verificationDeadline bounds one verification Job. The probe caps curl at
	// 10s; the deadline covers image pull and, crucially, turns a pod that is
	// never admitted (restricted Pod Security, an unpullable image) into a
	// Failed Job instead of leaving the Gateway stuck on Verifying.
	verificationDeadline = int64(120)

	// verifyContainerName is the name of the verification Job's container,
	// whose termination message carries the probe diagnostic.
	verifyContainerName = "verify"

	// componentGatewayVerification is the app.kubernetes.io/component value on
	// verification Jobs, and part of the selector that finds them again.
	componentGatewayVerification = "gateway-verification"

	// Ready-condition reasons for connectivity verification outcomes.
	reasonConnectionVerified   = "ConnectionVerified"
	reasonConnectionFailed     = "ConnectionFailed"
	reasonAuthenticationFailed = "AuthenticationFailed"
	reasonEndpointUnreachable  = "EndpointUnreachable"

	// reasonVerificationSkipped marks a Gateway the controller deliberately
	// did not probe, because no probe it can run would describe this Gateway
	// (see gatewayProbeSkip). Ready is True so the Gateway is usable;
	// ConnectionVerified stays false so status does not claim a check that
	// never ran.
	reasonVerificationSkipped = "VerificationSkipped"
)

// GatewayReconciler reconciles a Gateway object.
type GatewayReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// apiReader reads verification pods directly from the API server,
	// bypassing the cache. Two reasons the cache can't serve this read: the
	// manager's Pod cache is restricted to sandbox pods (see
	// SandboxPodCacheOptions), so verification pods are never cached; and even
	// if they were, the probe's termination message is written moments before
	// its Job flips to Complete/Failed, so the cached pod could still lack the
	// terminated container status. Set from mgr.GetAPIReader() in
	// SetupWithManager.
	apiReader client.Reader

	// VerificationImage overrides the container image used for
	// connectivity verification Jobs. Defaults to DefaultVerificationImage.
	VerificationImage string
}

// +kubebuilder:rbac:groups=konveyor.io,resources=gateways,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=konveyor.io,resources=gateways/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=konveyor.io,resources=gateways/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile handles Gateway reconciliation.
//
// The controller verifies gateway connectivity by:
//  1. Checking that the referenced credential Secret exists
//  2. Creating a verification Job that tests the endpoint using the
//     agent base image
//  3. Updating status based on the Job result
func (r *GatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var gateway konveyoriov1alpha1.Gateway
	if err := r.Get(ctx, req.NamespacedName, &gateway); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.V(1).Info("Reconciling Gateway", "name", gateway.Name)

	original := gateway.DeepCopy()
	gateway.Status.ObservedGeneration = gateway.Generation

	// Step 1: Check the credential Secret exists.
	secretKey := types.NamespacedName{
		Namespace: gateway.Namespace,
		Name:      gateway.Spec.CredentialRef.SecretName,
	}
	var secret corev1.Secret
	if err := r.Get(ctx, secretKey, &secret); err != nil {
		if errors.IsNotFound(err) {
			gateway.Status.ConnectionVerified = false
			meta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: gateway.Generation,
				Reason:             "CredentialSecretNotFound",
				Message:            fmt.Sprintf("Secret %q not found", gateway.Spec.CredentialRef.SecretName),
			})
			return r.patchStatus(ctx, &gateway, original)
		}
		return ctrl.Result{}, err
	}

	// Check the expected key exists in the Secret. A keyless credentialRef
	// means the whole Secret is the credential (multi-variable, e.g. AWS
	// SigV4) - then it just must not be empty.
	if gateway.Spec.CredentialRef.Key != "" {
		if _, ok := secret.Data[gateway.Spec.CredentialRef.Key]; !ok {
			gateway.Status.ConnectionVerified = false
			meta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: gateway.Generation,
				Reason:             "CredentialKeyNotFound",
				Message: fmt.Sprintf("Key %q not found in Secret %q",
					gateway.Spec.CredentialRef.Key, gateway.Spec.CredentialRef.SecretName),
			})
			return r.patchStatus(ctx, &gateway, original)
		}
	} else if len(secret.Data) == 0 {
		gateway.Status.ConnectionVerified = false
		meta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: gateway.Generation,
			Reason:             "CredentialSecretEmpty",
			Message: fmt.Sprintf("Secret %q has no data keys",
				gateway.Spec.CredentialRef.SecretName),
		})
		return r.patchStatus(ctx, &gateway, original)
	}

	credHash := credentialHash(gateway.Spec.CredentialRef, &secret)

	// settled: the Ready condition already speaks for this generation against
	// the credential currently in the Secret. A spec change (new generation)
	// or a credential rotation (new hash) makes it stale.
	readyCond := meta.FindStatusCondition(gateway.Status.Conditions, ConditionTypeReady)
	settled := readyCond != nil &&
		readyCond.ObservedGeneration == gateway.Generation &&
		gateway.Status.VerifiedCredentialHash == credHash

	// Step 2: A Gateway no probe can describe is not probed at all - the Job
	// could only ever fail, or would have to send a credential somewhere it
	// does not belong. This is decided ahead of the terminal early return
	// below on purpose: a controller from before the skip parks such a
	// Gateway at ConnectionFailed, which is terminal, and the early return
	// would then keep that stale verdict for good (#227). Drop any Job left
	// behind by a previous provider value and settle the Gateway as skipped.
	probe := providerProbeKind(gateway.Spec.Provider)
	if skipReason, skipped := gatewayProbeSkip(&gateway, probe, &secret); skipped {
		if settled && readyCond.Reason == reasonVerificationSkipped {
			return ctrl.Result{}, nil
		}
		if err := r.deleteVerificationJobs(ctx, &gateway, ""); err != nil {
			return ctrl.Result{}, err
		}
		// Ready, so the Gateway is usable and Agents referencing it can go
		// Ready - but not verified, because nothing was checked.
		gateway.Status.ConnectionVerified = false
		gateway.Status.VerifiedCredentialHash = credHash
		meta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: gateway.Generation,
			Reason:             reasonVerificationSkipped,
			Message:            skipReason,
		})
		return r.patchStatus(ctx, &gateway, original)
	}

	// Step 2b: If already verified (or failed) for the current generation and
	// the credential it was verified against, skip re-verification.
	//
	// VerificationSkipped is excluded: reaching here means a probe does now
	// describe this Gateway, so a skipped verdict left by an older controller
	// - every native aws-bedrock Gateway, before this - must be re-probed
	// rather than carried forward. The mirror of the #227 case above.
	if settled && isTerminalReadyReason(readyCond.Reason) &&
		readyCond.Reason != reasonVerificationSkipped {
		return ctrl.Result{}, nil
	}

	// Clean up verification Jobs from prior generations and prior credentials.
	// If a Gateway spec changes or its credential rotates while verification is
	// queued/running, the old Job is orphaned because completion events
	// reconcile under the new name.
	currentJobName := verificationJobName(&gateway, credHash)
	if err := r.deleteVerificationJobs(ctx, &gateway, currentJobName); err != nil {
		return ctrl.Result{}, err
	}

	// Step 3: Check for an existing verification Job.
	// Include generation in name to avoid collisions when re-verifying.
	jobName := currentJobName
	jobKey := types.NamespacedName{Namespace: gateway.Namespace, Name: jobName}
	var job batchv1.Job
	if err := r.Get(ctx, jobKey, &job); err != nil {
		if errors.IsNotFound(err) {
			// No verification Job exists - create one.
			if err := r.createVerificationJob(ctx, &gateway, jobName); err != nil {
				logger.Error(err, "Failed to create verification Job")
				return ctrl.Result{}, err
			}
			gateway.Status.ConnectionVerified = false
			meta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: gateway.Generation,
				Reason:             "Verifying",
				Message:            "Connectivity verification in progress",
			})
			return r.patchStatus(ctx, &gateway, original)
		}
		return ctrl.Result{}, err
	}

	// Step 3: Check the Job status.
	if isJobComplete(&job) {
		succeeded := isJobSucceeded(&job)

		// Read the probe's diagnostic from the pod's termination message
		// before deleting the Job, so the failure cause (HTTP code or
		// transport error) survives on status. Falls back to a generic
		// message when no diagnostic is available.
		diag := r.verificationDiagnostic(ctx, gateway.Namespace, jobName, job.UID)
		reason, message := gatewayVerifyReason(diag, gateway.Spec.Endpoint, jobName, succeeded, probe)

		status := metav1.ConditionFalse
		if succeeded {
			status = metav1.ConditionTrue
		}
		gateway.Status.ConnectionVerified = succeeded
		// Stamp the credential this outcome describes. Step 2 reads it back to
		// decide whether the settled result still applies.
		gateway.Status.VerifiedCredentialHash = credHash
		meta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             status,
			ObservedGeneration: gateway.Generation,
			Reason:             reason,
			Message:            message,
		})

		// Clean up the completed Job.
		if err := r.Delete(ctx, &job, client.PropagationPolicy(metav1.DeletePropagationBackground)); client.IgnoreNotFound(err) != nil {
			logger.Error(err, "Failed to delete verification Job")
		}
	} else {
		// Job still running.
		gateway.Status.ConnectionVerified = false
		meta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: gateway.Generation,
			Reason:             "Verifying",
			Message:            "Connectivity verification in progress",
		})
	}

	return r.patchStatus(ctx, &gateway, original)
}

// knownProviders is the set of provider identifiers (normalized form) the
// controller and harness recognize. Providers outside this set still verify
// with the OpenAI-compatible default, but the mismatch is logged — mirroring
// the fallthrough warning in providerEnv (harness/internal/goose/lifecycle.go).
var knownProviders = map[string]bool{
	providerAnthropic:  true,
	providerOpenAI:     true,
	providerXAI:        true,
	providerGCPVertex:  true,
	providerAWSBedrock: true,
}

// gatewayProbeKind is the verification probe a provider's API can answer.
type gatewayProbeKind int

const (
	// probeNone: nothing the controller can call would tell the truth about
	// this provider, so the Gateway settles as reasonVerificationSkipped.
	probeNone gatewayProbeKind = iota

	// probeModels: GET /v1/models with a bearer-style credential, the
	// OpenAI-compatible default every unrecognized provider also gets.
	probeModels

	// probeBedrockConverse: a SigV4-signed ConverseStream against
	// bedrock-runtime, which exercises the credential, the region, the model
	// id and bedrock:InvokeModelWithResponseStream - everything a run needs.
	probeBedrockConverse
)

// providerProbes maps a normalized provider to the probe its API can answer.
// A provider absent from the map gets probeModels; the controller logs the
// mismatch for one it does not recognize (see createVerificationJob),
// mirroring the fallthrough warning in providerEnv.
//
// gcp_vertex_ai stays unprobed: its credential is an ADC service account and
// a call needs an OAuth2 token minted from that account's RSA key, which is
// not something curl can do. Until that probe exists, skip-with-reason (#167)
// remains the honest answer for it.
//
// Kept in lockstep with the harness providerEnv switch
// (harness/internal/goose/lifecycle.go), where these same two providers are
// the ones that take neither an API key nor an endpoint.
var providerProbes = map[string]gatewayProbeKind{
	providerAWSBedrock: probeBedrockConverse,
	providerGCPVertex:  probeNone,
}

// providerProbeKind returns the probe to run for a provider.
func providerProbeKind(provider string) gatewayProbeKind {
	if kind, ok := providerProbes[normalizeProvider(provider)]; ok {
		return kind
	}
	return probeModels
}

// bedrockProbeSecretKeys are the Secret keys the SigV4 probe needs to sign a
// request. They are the static-credential names the AWS SDK reads, so a
// Secret that carries them is one the probe can reproduce exactly.
var bedrockProbeSecretKeys = []string{
	awsAccessKeyIDEnv,
	awsSecretAccessKeyEnv,
	awsRegionEnv,
}

// gatewayProbeSkip reports whether this Gateway is settled without being
// probed, and the status message saying why. Three cases, all of which leave
// the Gateway exactly where #167 left it - Ready, unverified, and honest
// about it - rather than failing a configuration that works:
//
//   - the provider has no probe the controller can speak (gcp_vertex_ai);
//   - a Bedrock Secret without the static SigV4 keys. A cluster that gets its
//     AWS credentials another way (IRSA, a web identity token) is a working
//     setup the probe pod cannot reproduce, so a failure here would be the
//     probe's, not the Gateway's. The message names the missing keys, which
//     also catches the common case: a Secret missing AWS_REGION;
//   - a Bedrock Gateway pointed somewhere other than bedrock-runtime. The
//     probe signs with the AWS credential and sends a session token when one
//     is present, so an arbitrary spec.endpoint would be handed usable
//     credential material (#102). Refusing to sign for a non-AWS host keeps
//     that out of reach.
func gatewayProbeSkip(
	gateway *konveyoriov1alpha1.Gateway,
	probe gatewayProbeKind,
	secret *corev1.Secret,
) (string, bool) {
	switch probe {
	case probeNone:
		return fmt.Sprintf(
			"Provider %q has no API surface the verification probe can speak; "+
				"endpoint and credentials are unverified",
			gateway.Spec.Provider), true

	case probeBedrockConverse:
		if missing := missingSecretKeys(secret, bedrockProbeSecretKeys); len(missing) > 0 {
			return fmt.Sprintf(
				"Secret %q does not carry %s, so the SigV4 probe cannot sign a "+
					"request; endpoint and credentials are unverified",
				gateway.Spec.CredentialRef.SecretName, strings.Join(missing, ", ")), true
		}
		if !isBedrockRuntimeEndpoint(gateway.Spec.Endpoint) {
			return fmt.Sprintf(
				"Endpoint %q is not an AWS bedrock-runtime endpoint, and the probe "+
					"will not sign an AWS credential for another host; endpoint and "+
					"credentials are unverified",
				gateway.Spec.Endpoint), true
		}
	}

	return "", false
}

// missingSecretKeys returns the wanted keys the Secret does not hold, in the
// order they were asked for. A key present but empty counts as missing: an
// empty AWS_REGION signs a request into a credential scope that cannot exist.
func missingSecretKeys(secret *corev1.Secret, want []string) []string {
	var missing []string
	for _, key := range want {
		if len(secret.Data[key]) == 0 {
			missing = append(missing, key)
		}
	}
	return missing
}

// isBedrockRuntimeEndpoint reports whether the endpoint is an AWS-operated
// bedrock-runtime address the probe may sign a request for. Deliberately
// narrow: it is the gate that keeps a signed AWS credential - and the session
// token the probe forwards with it - from reaching an endpoint someone put in
// a Gateway spec. The shapes allowed are the public regional endpoints
// (bedrock-runtime.<region>.amazonaws.com), their FIPS and China variants,
// and PrivateLink addresses, all of which carry "bedrock" in a host under an
// AWS domain.
func isBedrockRuntimeEndpoint(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if !strings.Contains(host, "bedrock") {
		return false
	}
	for _, suffix := range []string{".amazonaws.com", ".amazonaws.com.cn"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// normalizeProvider lowercases and converts hyphens to underscores so provider
// matching stays in lockstep with the harness (providerEnv in
// harness/internal/goose/lifecycle.go), which keys off the same normalization.
func normalizeProvider(provider string) string {
	return strings.ReplaceAll(strings.ToLower(provider), "-", "_")
}

// gatewayVerificationCommand builds the shell command for a probe kind.
// probeNone never reaches here: such a Gateway settles without a Job.
func gatewayVerificationCommand(probe gatewayProbeKind, provider string, includeAuth bool) string {
	if probe == probeBedrockConverse {
		return gatewayVerificationBedrockCommand()
	}
	return gatewayVerificationCurlCommand(provider, includeAuth)
}

// gatewayVerificationBedrockCommand builds the shell command for the
// aws-bedrock probe: a SigV4-signed ConverseStream capped at one output
// token. curl 8 signs with --aws-sigv4 (the verification image ships 8.12),
// which is what keeps this a one-container-shells-curl probe like the other
// one rather than a new signing binary in the Job.
//
// The diagnostic vocabulary is the models probe's, plus the provider's own
// error body as a free-text detail= tail (see diagDetail):
//   - "ok code=<n>"                      a 2xx - the credential invoked the model
//   - "auth code=<n> detail=<body>"      401/403 - rejected or denied by IAM/SCP
//   - "http code=<n> detail=<body>"      any other non-2xx
//   - "unreachable rc=<n>"               curl transport error
//
// Carrying the body is the point of the whole change: an SCP denial answers
// 403 with the operation it refused ("not authorized to perform:
// bedrock:InvokeModelWithResponseStream on ..."), and that sentence is what
// the Gateway status should say. The body names an IAM principal, never the
// secret - and nothing is written anywhere but the termination log.
//
// Credentials stay in env vars the same way the models probe keeps them: the
// Secret is mounted whole via envFrom, so no value is interpolated into the
// command string. curl takes SigV4 keys only through -u, so they do reach
// this process's argv inside its own pod - where the same values are already
// in the environment - and nowhere else.
func gatewayVerificationBedrockCommand() string {
	// A session token is part of the credential, not a header curl can be
	// handed empty: an empty x-amz-security-token would still be signed and
	// sent, and AWS rejects it. Build it as an argument only when set.
	curl := `curl -s --max-time 10` +
		` --aws-sigv4 "aws:amz:$` + awsRegionEnv + `:` + bedrockSigningService + `"` +
		` -u "$` + awsAccessKeyIDEnv + `:$` + awsSecretAccessKeyEnv + `"` +
		` -H "content-type: application/json"` +
		` -d '` + bedrockProbeBody + `'` +
		` -w '\n%{http_code}' "$@" "` + endpointBedrockConverseProbe + `"`

	return `set --
if [ -n "$AWS_SESSION_TOKEN" ]; then set -- -H "x-amz-security-token: $AWS_SESSION_TOKEN"; fi
resp=$(` + curl + `); rc=$?
if [ "$rc" -ne 0 ]; then echo "unreachable rc=$rc" > /dev/termination-log; exit 1; fi
code=$(printf '%s' "$resp" | tail -n1)
if echo "$code" | grep -qE '` + verificationHTTPCodePattern + `'; then echo "ok code=$code" > /dev/termination-log; exit 0; fi
detail=$(printf '%s' "$resp" | sed '$d' | tr -d '\r' | tr '\n' ' ' | cut -c1-` + strconv.Itoa(verificationDetailLimit) + `)
case "$code" in
401|403) printf 'auth code=%s detail=%s\n' "$code" "$detail" > /dev/termination-log ;;
*) printf 'http code=%s detail=%s\n' "$code" "$detail" > /dev/termination-log ;;
esac
exit 1`
}

// gatewayVerificationCurlCommand builds the shell command used by the
// verification Job. Anthropic's native API requires x-api-key +
// anthropic-version, so it gets a dedicated auth header; every other provider
// uses the OpenAI-compatible default (Authorization: Bearer against
// /v1/models). When includeAuth is false the auth header is omitted so keyless
// gateways are probed for reachability without an empty credential.
//
// The command captures curl's HTTP code and exit status and writes a compact
// diagnostic to the pod's termination message (/dev/termination-log) so the
// controller can surface the failure cause on the Gateway status:
//   - "ok code=<n>"        a 2xx response (exit 0)
//   - "auth code=<n>"      HTTP 401/403 - bad or missing credential
//   - "http code=<n>"      any other non-2xx response
//   - "unreachable rc=<n>" curl transport error (DNS, refused, timeout, ...)
//
// The endpoint and key stay in env vars ($LLM_ENDPOINT, $LLM_API_KEY) and are
// never interpolated into the command string, keeping the probe injection-safe.
func gatewayVerificationCurlCommand(provider string, includeAuth bool) string {
	curl := "curl -sk --max-time 10 -o /dev/null -w '%{http_code}'"
	if includeAuth {
		curl += gatewayVerificationAuthHeader(provider)
	}
	curl += ` "` + endpointModelsProbe + `"`

	// Classify 401/403 as an authentication failure only when the probe
	// actually sent a credential. A keyless credentialRef (empty key, e.g.
	// AWS SigV4) sends no Authorization header, so a 401/403 there is just
	// another non-2xx and must not tell the user to check an API key that
	// isn't in the secret.
	authArm := ""
	if includeAuth {
		authArm = `
401|403) echo "auth code=$code" > /dev/termination-log ;;`
	}

	return "code=$(" + curl + `); rc=$?
if [ "$rc" -ne 0 ]; then echo "unreachable rc=$rc" > /dev/termination-log; exit 1; fi
if echo "$code" | grep -qE '` + verificationHTTPCodePattern + `'; then echo "ok code=$code" > /dev/termination-log; exit 0; fi
case "$code" in` + authArm + `
*) echo "http code=$code" > /dev/termination-log ;;
esac
exit 1`
}

// gatewayVerificationAuthHeader returns the provider-specific auth header
// snippet (curl -H flags). Only Anthropic deviates from the OpenAI-compatible
// default (Authorization: Bearer) today, because its native API rejects
// Bearer; add new deviating providers as cases here.
func gatewayVerificationAuthHeader(provider string) string {
	switch normalizeProvider(provider) {
	case providerAnthropic:
		return ` -H "x-api-key: $LLM_API_KEY" -H "anthropic-version: ` + anthropicAPIVersion + `"`
	default:
		return ` -H "Authorization: Bearer $LLM_API_KEY"`
	}
}

// deleteVerificationJobs deletes this Gateway's verification Jobs except the
// one named keep. Pass an empty keep to delete all of them.
func (r *GatewayReconciler) deleteVerificationJobs(
	ctx context.Context,
	gateway *konveyoriov1alpha1.Gateway,
	keep string,
) error {
	var jobs batchv1.JobList
	if err := r.List(ctx, &jobs,
		client.InNamespace(gateway.Namespace),
		// Scoped to this controller's verification Jobs: the loop below deletes
		// what it finds, so the Gateway label alone is too broad a net.
		client.MatchingLabels{
			labelManagedBy: managedByLabel,
			labelComponent: componentGatewayVerification,
			labelGateway:   gatewayLabelValue(gateway),
		},
	); err != nil {
		return err
	}
	for i := range jobs.Items {
		if jobs.Items[i].Name == keep {
			continue
		}
		if err := r.Delete(ctx, &jobs.Items[i],
			client.PropagationPolicy(metav1.DeletePropagationBackground),
		); client.IgnoreNotFound(err) != nil {
			log.FromContext(ctx).V(1).Info("Failed to delete stale verification Job",
				"job", jobs.Items[i].Name)
		}
	}
	return nil
}

// createVerificationJob creates a Job that verifies connectivity to the
// gateway endpoint using the agent base image.
func (r *GatewayReconciler) createVerificationJob(
	ctx context.Context,
	gateway *konveyoriov1alpha1.Gateway,
	jobName string,
) error {
	image := r.VerificationImage
	if image == "" {
		image = DefaultVerificationImage
	}

	// The verification Job runs a single curl against the endpoint. The agent
	// base image includes curl. Only 2xx counts as success so 401/403
	// (invalid or missing credential) fail verification instead of marking
	// ConnectionVerified.
	probe := providerProbeKind(gateway.Spec.Provider)

	// Keyless credentialRef (empty key, e.g. AWS SigV4) omits the auth header
	// entirely for the models probe - an empty credential would 401 under the
	// ^2 check. The Bedrock probe ignores it: SigV4 spans several variables,
	// so the credential always arrives through envFrom.
	includeAuth := probe != probeBedrockConverse && gateway.Spec.CredentialRef.Key != ""
	if probe == probeModels && !knownProviders[normalizeProvider(gateway.Spec.Provider)] {
		log.FromContext(ctx).Info(
			"unrecognized gateway provider; verifying with default "+
				"Authorization: Bearer against /v1/models",
			"provider", gateway.Spec.Provider,
		)
	}
	env := []corev1.EnvVar{{Name: "LLM_ENDPOINT", Value: gateway.Spec.Endpoint}}
	var envFrom []corev1.EnvFromSource
	switch {
	case probe == probeBedrockConverse:
		// The whole Secret, with its own keys as the variable names - the
		// same shape the run gets (see the Gateway credential block in
		// buildSandboxEnv), so the probe signs with exactly what goose will.
		// The model id is spec data rather than a credential, but it travels
		// as an env var too so the command string stays free of anything
		// user-supplied.
		env = append(env, corev1.EnvVar{Name: "LLM_MODEL", Value: gateway.Spec.Model.Name})
		envFrom = []corev1.EnvFromSource{{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: gateway.Spec.CredentialRef.SecretName,
				},
			},
		}}
	case includeAuth:
		env = append(env, corev1.EnvVar{
			Name: "LLM_API_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: gateway.Spec.CredentialRef.SecretName,
					},
					Key: gateway.Spec.CredentialRef.Key,
				},
			},
		})
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: gateway.Namespace,
			Labels: map[string]string{
				labelManagedBy: managedByLabel,
				labelComponent: componentGatewayVerification,
				labelGateway:   gatewayLabelValue(gateway),
			},
		},
		Spec: batchv1.JobSpec{
			// One shot; a bad endpoint or credential will not fix itself on retry.
			BackoffLimit: ptr.To(int32(0)),
			// A pod that is never admitted (restricted Pod Security, an
			// unpullable image) leaves the Job neither Complete nor Failed. The
			// deadline turns that into a Failed Job so the Gateway does not sit
			// on Verifying forever.
			ActiveDeadlineSeconds: ptr.To(verificationDeadline),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					// Admissible under the restricted Pod Security Standards,
					// matching the enumeration Job and the manager's own pod.
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptr.To(true),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{
						{
							Name:            verifyContainerName,
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							// The probe only reads; it writes solely to
							// /dev/null and /dev/termination-log, both on the
							// writable /dev mount, so a read-only root is fine.
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: ptr.To(false),
								ReadOnlyRootFilesystem:   ptr.To(true),
								Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
							},
							Command: []string{
								"sh", "-c",
								// Use env vars to avoid shell injection.
								gatewayVerificationCommand(probe, gateway.Spec.Provider, includeAuth),
							},
							Env:     env,
							EnvFrom: envFrom,
						},
					},
				},
			},
		},
	}

	// Set owner reference so the Job is cleaned up with the gateway.
	if err := ctrl.SetControllerReference(gateway, job, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference: %w", err)
	}

	return r.Create(ctx, job)
}

// patchStatus patches the Gateway status and returns a reconcile result.
func (r *GatewayReconciler) patchStatus(
	ctx context.Context,
	gateway *konveyoriov1alpha1.Gateway,
	original *konveyoriov1alpha1.Gateway,
) (ctrl.Result, error) {
	if err := r.Status().Patch(ctx, gateway, client.MergeFrom(original)); err != nil {
		log.FromContext(ctx).Error(err, "Failed to patch Gateway status")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// isJobComplete returns true if the Job has a Complete or Failed condition.
func isJobComplete(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if (c.Type == batchv1.JobComplete || c.Type == batchv1.JobFailed) &&
			c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// isJobSucceeded returns true if the Job has a Complete condition.
func isJobSucceeded(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// verificationJobName is derived from the Gateway's generation and the hash of
// the credential it points at, so both a spec change and a credential rotation
// start a fresh Job instead of reading the previous generation's answer back
// off a Job that has not finished being deleted yet.
//
// A Job name over 63 characters cannot be a label value, and the Job controller
// stamps the name into batch.kubernetes.io/job-name on every pod it creates, so
// a long Gateway name would otherwise produce a Job whose pods are rejected.
// sanitizeVolumeName bounds it, disambiguating the truncated names with a hash
// of the full one - the same treatment enumerationJobName gets.
func verificationJobName(gateway *konveyoriov1alpha1.Gateway, credHash string) string {
	return sanitizeVolumeName(fmt.Sprintf("%s%s-gen%d-%s",
		verificationJobPrefix, gateway.Name, gateway.Generation,
		shortCredentialHash(credHash)))
}

// gatewayLabelValue is the labelGateway value stamped on a Gateway's
// verification Job. A label value caps at 63 characters, but a Gateway name is a
// DNS subdomain and can run to 253 - passing the raw name through would have the
// API server reject the Job on create, and reject the selector that looks it up.
func gatewayLabelValue(gateway *konveyoriov1alpha1.Gateway) string {
	return sanitizeVolumeName(gateway.Name)
}

// credentialHash digests the credential a credentialRef selects, so the
// controller can tell a rotated credential from an unchanged one. Rotation does
// not bump the Gateway generation, so without this a re-reconcile would find
// the settled Ready condition and skip re-verification.
//
// Only the selected material is hashed: for a keyed credentialRef that is the
// one key's value, so unrelated keys in a shared Secret do not churn
// verification; for a keyless one (multi-variable, e.g. AWS SigV4) the whole
// Secret is the credential, so every key and value counts.
//
// The full digest is what lands in status and what the skip decision compares.
// Only the Job name needs a short form (see verificationJobName), and there is
// no reason to let that naming limit weaken the comparison.
func credentialHash(ref konveyoriov1alpha1.GatewayCredentialRef, secret *corev1.Secret) string {
	h := sha256.New()
	if ref.Key != "" {
		hashCredentialField(h, []byte(ref.Key))
		hashCredentialField(h, secret.Data[ref.Key])
	} else {
		keys := make([]string, 0, len(secret.Data))
		for k := range secret.Data {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			hashCredentialField(h, []byte(k))
			hashCredentialField(h, secret.Data[k])
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// shortCredentialHash is the part of a credential digest that goes into the
// verification Job name, which has 63 characters to spend. Collisions here only
// mean two Jobs could share a name; the skip decision compares the full digest.
func shortCredentialHash(credHash string) string {
	if len(credHash) <= 8 {
		return credHash
	}
	return credHash[:8]
}

// hashCredentialField length-prefixes b before hashing it, so that {"ab": "c"} and
// {"a": "bc"} cannot produce the same digest.
func hashCredentialField(h hash.Hash, b []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(b)))
	_, _ = h.Write(n[:])
	_, _ = h.Write(b)
}

// isTerminalReadyReason reports whether a Ready-condition reason represents a
// settled verification outcome. The reconciler skips re-verifying a Gateway
// whose current generation already reached one of these against the credential
// currently in the Secret; a spec change (new generation) or a credential
// rotation (new hash) re-triggers verification.
func isTerminalReadyReason(reason string) bool {
	switch reason {
	case reasonConnectionVerified, reasonConnectionFailed, reasonAuthenticationFailed,
		reasonEndpointUnreachable, reasonVerificationSkipped:
		return true
	}
	return false
}

// verificationDiagnostic returns the termination message written by the
// verification Job's pod (see gatewayVerificationCurlCommand), or "" if no
// terminated pod/message is found. It prefers the "verify" container and falls
// back to any other terminated container carrying a message. Pods are pinned to
// the Job by controller UID so a stray pod carrying the same job-name label
// cannot win. Reads are uncached (see the apiReader field).
func (r *GatewayReconciler) verificationDiagnostic(ctx context.Context, namespace, jobName string, jobUID types.UID) string {
	var pods corev1.PodList
	if err := r.apiReader.List(ctx, &pods,
		client.InNamespace(namespace),
		client.MatchingLabels{
			batchv1.JobNameLabel:       jobName,
			batchv1.ControllerUidLabel: string(jobUID),
		},
	); err != nil {
		// An RBAC gap or API error looks the same as "the probe wrote
		// nothing" to the caller (both yield the generic terminal message), so
		// surface it here rather than letting it vanish.
		log.FromContext(ctx).Error(err, "Failed to list verification pods for diagnostic", "job", jobName)
		return ""
	}

	var fallback string
	for i := range pods.Items {
		for _, cs := range pods.Items[i].Status.ContainerStatuses {
			if cs.State.Terminated == nil {
				continue
			}
			msg := strings.TrimSpace(cs.State.Terminated.Message)
			if msg == "" {
				continue
			}
			if cs.Name == verifyContainerName {
				return msg
			}
			fallback = msg
		}
	}
	return fallback
}

// gatewayVerifyReason maps a probe diagnostic line to a Ready-condition reason
// and a human-readable message. succeeded is the Job's own Complete/Failed
// outcome and takes precedence for the status; the diagnostic only enriches the
// message and refines the failure reason. probe says which probe wrote the
// diagnostic, so the message names the surface actually called. When diag is
// empty or unrecognized it falls back to the historical generic messages.
func gatewayVerifyReason(
	diag, endpoint, jobName string,
	succeeded bool,
	probe gatewayProbeKind,
) (reason, message string) {
	fields := strings.Fields(diag)
	code := diagValue(fields, "code")

	if succeeded {
		// Only quote the code when the probe reported success ("ok code=<n>");
		// a mismatched or empty diagnostic must not produce "reachable (HTTP 401)".
		if len(fields) > 0 && fields[0] == "ok" && code != "" {
			return reasonConnectionVerified, fmt.Sprintf("%s (HTTP %s)", probeSuccessPhrase(probe, endpoint), code)
		}
		return reasonConnectionVerified, probeSuccessPhrase(probe, endpoint)
	}

	// The provider's own error text, when the probe captured one. This is
	// what carries an IAM or SCP denial - naming the operation it refused -
	// onto the Gateway status.
	var detail string
	if d := diagDetail(diag); d != "" {
		detail = ": " + d
	}

	if len(fields) > 0 {
		switch fields[0] {
		case "auth":
			if code != "" {
				return reasonAuthenticationFailed, fmt.Sprintf(
					"%s returned HTTP %s - %s%s",
					probeSurface(probe, endpoint), code, probeAuthAdvice(probe), detail)
			}
		case "http":
			if code != "" {
				return reasonConnectionFailed, fmt.Sprintf(
					"%s returned HTTP %s%s", probeSurface(probe, endpoint), code, detail)
			}
		case "unreachable":
			return reasonEndpointUnreachable, fmt.Sprintf(
				"Endpoint %s is unreachable (%s)", endpoint, curlErrorPhrase(diagValue(fields, "rc")))
		}
	}

	return reasonConnectionFailed, fmt.Sprintf("Verification Job %q failed", jobName)
}

// probeSurface names what the probe actually called, so a failure message
// points at the request that produced it rather than at the bare endpoint.
func probeSurface(probe gatewayProbeKind, endpoint string) string {
	if probe == probeBedrockConverse {
		return "Bedrock ConverseStream at " + endpoint
	}
	return "Endpoint " + endpoint + "/v1/models"
}

// probeSuccessPhrase says what a passing probe actually established. The
// models probe proves reachability and a credential the endpoint accepts; the
// Bedrock probe proves the stronger thing, that the credential invoked the
// configured model.
func probeSuccessPhrase(probe gatewayProbeKind, endpoint string) string {
	if probe == probeBedrockConverse {
		return fmt.Sprintf("Endpoint %s accepted a signed ConverseStream call for the configured model", endpoint)
	}
	return fmt.Sprintf("Endpoint %s is reachable", endpoint)
}

// probeAuthAdvice is the "what to check" half of an AuthenticationFailed
// message. Bedrock's 403 covers two different mistakes - a credential the
// account rejects and one an IAM policy or SCP denies - so it names the
// permission as well as the Secret.
func probeAuthAdvice(probe gatewayProbeKind) string {
	if probe == probeBedrockConverse {
		return "check the AWS credential in the Secret, and that it is allowed " +
			"bedrock:InvokeModelWithResponseStream on this model"
	}
	return "check the credential secret's API key"
}

// diagValue extracts the value of a "key=value" token from a diagnostic line's
// fields, or "" if absent.
func diagValue(fields []string, key string) string {
	prefix := key + "="
	for _, f := range fields {
		if v, ok := strings.CutPrefix(f, prefix); ok {
			return v
		}
	}
	return ""
}

// diagDetail returns the free-text tail a probe appended as " detail=<text>",
// or "" when there is none. Unlike diagValue it keeps spaces, because the tail
// is a provider error sentence rather than a token - which is also why it is
// always written last on the diagnostic line.
func diagDetail(diag string) string {
	_, tail, ok := strings.Cut(diag, diagDetailPrefix)
	if !ok {
		return ""
	}
	return strings.TrimSpace(tail)
}

// curlErrorPhrase turns a curl exit code into a human-readable cause.
func curlErrorPhrase(rc string) string {
	switch rc {
	case "6":
		return "curl exit 6: could not resolve host"
	case "7":
		return "curl exit 7: connection refused"
	case "28":
		return "curl exit 28: timed out"
	case "":
		return "connection error"
	default:
		return "curl exit " + rc + ": transport error"
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *GatewayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Uncached reader for verification pods; see the apiReader field.
	r.apiReader = mgr.GetAPIReader()

	// Reverse lookup from a credential Secret to the Gateways using it.
	if err := mgr.GetFieldIndexer().IndexField(context.Background(),
		&konveyoriov1alpha1.Gateway{}, gatewayCredentialSecretIndexField,
		func(obj client.Object) []string {
			gateway := obj.(*konveyoriov1alpha1.Gateway)
			if gateway.Spec.CredentialRef.SecretName == "" {
				return nil
			}
			return []string{gateway.Spec.CredentialRef.SecretName}
		},
	); err != nil {
		return fmt.Errorf("indexing %s: %w", gatewayCredentialSecretIndexField, err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&konveyoriov1alpha1.Gateway{}).
		Owns(&batchv1.Job{}).
		// Without this a deleted or rotated credential leaves the Gateway
		// reading Ready until something else happens to it. The Secret is
		// already in the manager cache - Reconcile Gets it through the cached
		// client - so the watch adds no new informer.
		Watches(&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.findGatewaysForSecret)).
		Named("gateway").
		Complete(r)
}

// findGatewaysForSecret maps a Secret event to the Gateways whose credentialRef
// names it.
func (r *GatewayReconciler) findGatewaysForSecret(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	var gateways konveyoriov1alpha1.GatewayList
	if err := r.List(ctx, &gateways,
		client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{gatewayCredentialSecretIndexField: obj.GetName()},
	); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list Gateways for Secret",
			"secret", obj.GetName(), "namespace", obj.GetNamespace())
		return nil
	}

	requests := make([]reconcile.Request, len(gateways.Items))
	for i, gateway := range gateways.Items {
		requests[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: gateway.Namespace,
				Name:      gateway.Name,
			},
		}
	}
	return requests
}
