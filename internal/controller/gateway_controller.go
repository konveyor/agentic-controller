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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

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

	// bedrockConverseStreamOperation is the bedrock-runtime operation the
	// probe calls. ConverseStream rather than Converse on purpose: goose
	// streams, so this is the operation a run actually performs and the one
	// an IAM policy or SCP has to allow
	// (bedrock:InvokeModelWithResponseStream).
	//
	// The controller builds and signs the whole URL (signBedrockProbeRequest),
	// so neither the operation, the host nor the model id is interpolated into
	// the probe's command string. The host is not spec.endpoint: it is the
	// bedrock-runtime address the run itself will reach, derived from the
	// credential Secret by bedrockProbeEndpoint, because a Bedrock run ignores
	// spec.endpoint entirely (the AWS SDK builds the host from AWS_REGION -
	// see providerEnv in harness/internal/goose/lifecycle.go).
	bedrockConverseStreamOperation = "converse-stream"

	// bedrockErrorBodyPattern marks a response body as an AWS error even
	// when the status line said 2xx: "__type" is how the coral stack names
	// an exception, and every AWS error type ends in "Exception". A real
	// ConverseStream success is an application/vnd.amazon.eventstream whose
	// events carry neither. This is the second guard on the false pass
	// above: the encoding closes the known trigger, and this closes the
	// shape of it. A one-token completion that happened to be the word
	// "Exception" would fail the probe instead, which is the safe direction
	// to be wrong in.
	bedrockErrorBodyPattern = `__type|Exception`

	// envLLMEndpoint carries the address the probe calls: spec.endpoint for
	// the models probe, and the resolved bedrock-runtime host for Bedrock
	// (see bedrockProbeEndpoint).
	envLLMEndpoint = "LLM_ENDPOINT"

	// awsSigV4Algorithm is the SigV4 algorithm name, used both in the string
	// to sign and in the Authorization header.
	awsSigV4Algorithm = "AWS4-HMAC-SHA256"

	// Env vars carrying the controller-signed request into the probe pod. The
	// signature covers the URL and the date, so all three travel together.
	envProbeURL           = "PROBE_URL"
	envSigV4Authorization = "SIGV4_AUTHORIZATION"
	envSigV4Date          = "SIGV4_DATE"
	envSigV4SecurityToken = "SIGV4_SECURITY_TOKEN"

	// bedrockSkewBodyPattern marks a 403 that is about time rather than about
	// the credential: the signature aged out, or the controller's clock
	// disagrees with AWS's. Reporting those as AuthenticationFailed would send
	// someone to check a credential that is fine - the same class of
	// misleading verdict this probe exists to remove.
	bedrockSkewBodyPattern = `Signature expired|RequestTimeTooSkewed|too skewed`

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

	// awsSessionTokenEnv is the temporary-credential token. It is a signed
	// header when present, so the probe cannot omit it.
	awsSessionTokenEnv = "AWS_SESSION_TOKEN"

	// AWS SDK endpoint overrides, in the precedence the SDK applies them.
	// A Secret carrying one of these is a Secret whose runs reach Bedrock
	// somewhere other than the public regional host - a PrivateLink or
	// FIPS address - so the probe has to follow it to probe what the run
	// probes (see bedrockProbeEndpoint).
	awsBedrockEndpointURLEnv = "AWS_ENDPOINT_URL_BEDROCK_RUNTIME"
	awsEndpointURLEnv        = "AWS_ENDPOINT_URL"

	// bedrockRuntimeHostPrefix and the AWS DNS suffixes are how the SDK
	// builds a bedrock-runtime host from a region, and so how the probe
	// does. The China partition uses its own suffix.
	bedrockRuntimeHostPrefix = "bedrock-runtime."
	awsDNSSuffix             = ".amazonaws.com"
	awsDNSSuffixChina        = ".amazonaws.com.cn"
	awsChinaRegionPrefix     = "cn-"

	// AWS endpoint-variant settings. goose's AWS config loader honours both,
	// and each selects a different host from the one bedrockProbeEndpoint
	// builds - dual-stack moves to .api.aws, FIPS to a -fips host. Rather
	// than reproduce those rules unverified, a Secret that sets either is
	// left unprobed (see gatewayProbeSkip): a probe calling a host the run
	// does not use is the defect this probe exists to avoid, and on a
	// network restricted to the configured endpoint it would fail a Gateway
	// whose runs are fine.
	awsUseFIPSEndpointEnv      = "AWS_USE_FIPS_ENDPOINT"
	awsUseDualStackEndpointEnv = "AWS_USE_DUALSTACK_ENDPOINT"

	// redactedPlaceholder stands in for endpoint userinfo and query values on
	// a Gateway's status (see redactEndpoint).
	redactedPlaceholder = "REDACTED"

	// verificationDetailLimit bounds the provider error text a probe copies
	// into its termination message, which the kubelet caps at 4KiB.
	//
	// 300 was too tight to diagnose with: AWS's SignatureDoesNotMatch names
	// the canonical string it expected, which is the whole diagnosis, and it
	// starts past 300 characters. 2000 leaves room for that and still sits
	// well inside both the kubelet's cap and a condition message.
	verificationDetailLimit = 2000

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
		// The message is compared along with the reason: a Gateway skipped by
		// an older controller is settled against a wording that no longer
		// says why, and on reason alone it would keep that stale sentence
		// until its generation or credential changed.
		if settled && readyCond.Reason == reasonVerificationSkipped &&
			readyCond.Message == skipReason {
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

	// The address the probe will actually call. For every provider but
	// Bedrock that is spec.endpoint; for Bedrock it comes from the credential
	// Secret, because that is where a Bedrock run's endpoint comes from too
	// (see bedrockProbeEndpoint). Status messages name this rather than
	// spec.endpoint, so a failure points at the host that produced it.
	probeEndpoint := gateway.Spec.Endpoint
	if probe == probeBedrockConverse {
		probeEndpoint = bedrockProbeEndpoint(&secret)
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
			// Sign the Bedrock request here rather than in the pod: the curl
			// this image ships cannot build SigV4's canonical URI correctly
			// (see signBedrockProbeRequest). Signed at Job creation, and the
			// Job's own deadline bounds how stale the signature can get.
			var signed bedrockProbeRequest
			if probe == probeBedrockConverse {
				var err error
				signed, err = signBedrockProbeRequest(
					&secret, probeEndpoint, gateway.Spec.Model.Name, time.Now())
				if err != nil {
					logger.Error(err, "Failed to sign the Bedrock verification request")
					return ctrl.Result{}, err
				}
			}
			// No verification Job exists - create one.
			if err := r.createVerificationJob(ctx, &gateway, jobName, probeEndpoint, signed); err != nil {
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
		reason, message := gatewayVerifyReason(diag, probeEndpoint, jobName, succeeded, probe)

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
//   - a Bedrock Secret the probe cannot turn into a bedrock-runtime address:
//     an AWS_REGION that is not a region name, or an endpoint override that
//     is not an https URL. Both would otherwise put something unchecked from
//     the Secret into the URL the probe signs for, and neither can be the
//     Gateway's fault in a way a failure would help with;
//   - a Bedrock Secret that selects an endpoint variant the probe does not
//     reproduce (AWS_USE_FIPS_ENDPOINT, AWS_USE_DUALSTACK_ENDPOINT), or an
//     override shape the signer cannot sign exactly. Probing the plain
//     regional host when the run uses a FIPS or dual-stack one would check a
//     host the run never calls, and on a network restricted to the configured
//     endpoint would fail a Gateway whose runs are fine.
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
		if override := bedrockEndpointOverride(secret); override != "" && !isHTTPSURL(override) {
			return fmt.Sprintf(
				"Secret %q sets an endpoint override that is not an https URL (%q), "+
					"and the probe will not send a signed AWS credential in the clear; "+
					"endpoint and credentials are unverified",
				gateway.Spec.CredentialRef.SecretName, redactEndpoint(override)), true
		}
		if region := bedrockProbeRegion(secret); !isAWSRegionName(region) {
			return fmt.Sprintf(
				"Secret %q carries %s=%q, which is not an AWS region name, so the "+
					"probe cannot address bedrock-runtime; endpoint and credentials "+
					"are unverified",
				gateway.Spec.CredentialRef.SecretName, awsRegionEnv, region), true
		}
		// An endpoint variant the probe does not resolve. Declining is the
		// point: the probe would otherwise call the plain regional host while
		// the run calls a FIPS or dual-stack one.
		if variants := setBedrockEndpointVariants(secret); len(variants) > 0 {
			return fmt.Sprintf(
				"Secret %q sets %s, which moves the endpoint somewhere this probe "+
					"does not resolve, so it would check a host the run does not use; "+
					"endpoint and credentials are unverified",
				gateway.Spec.CredentialRef.SecretName, strings.Join(variants, " and ")), true
		}
		// An override the signer cannot reproduce byte for byte. A base path
		// is fine and is signed; a query, a fragment or embedded credentials
		// are not, and a signature that cannot match is worse than no probe.
		if override := bedrockEndpointOverride(secret); override != "" &&
			!isSignableEndpointOverride(override) {
			return fmt.Sprintf(
				"Secret %q sets an endpoint override the probe cannot sign exactly "+
					"(%q); endpoint and credentials are unverified",
				gateway.Spec.CredentialRef.SecretName, redactEndpoint(override)), true
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

// bedrockProbeEndpoint returns the bedrock-runtime address the probe calls:
// the one the run itself will reach, resolved the way the AWS SDK resolves it
// and from the same input.
//
// spec.endpoint is deliberately not consulted. A Bedrock run never uses it -
// the SDK builds the host from AWS_REGION (providerEnv in
// harness/internal/goose/lifecycle.go), and getting-started says as much - so
// a probe signing for spec.endpoint would check a host no run talks to, which
// is the objection this PR raises against ListFoundationModels. Worse, it
// could fail a Gateway that works: the credential scope comes from the
// Secret's AWS_REGION while the host came from the spec, and AWS rejects a
// signature whose scope region disagrees with the endpoint it reached
// ("Credential should be scoped to a valid region"), so a Gateway left on the
// sample's us-east-1 endpoint with a eu-west-1 Secret would be parked at
// AuthenticationFailed with every referencing Agent held at
// DependenciesNotReady - for a Gateway whose runs are fine.
//
// Deriving the host from the Secret also closes the #102 exfiltration path
// outright rather than narrowing it: there is no longer any route from an
// attacker-chosen spec.endpoint to a host the probe will sign an AWS
// credential for. The only remaining input is the Secret, which is where the
// credential already lives - whoever can write it holds the credential, and a
// run already sends it wherever the Secret's endpoint override points.
func bedrockProbeEndpoint(secret *corev1.Secret) string {
	if override := bedrockEndpointOverride(secret); override != "" {
		return override
	}
	region := bedrockProbeRegion(secret)
	suffix := awsDNSSuffix
	if strings.HasPrefix(region, awsChinaRegionPrefix) {
		suffix = awsDNSSuffixChina
	}
	return "https://" + bedrockRuntimeHostPrefix + region + suffix
}

// bedrockEndpointOverride returns the endpoint the AWS SDK would use instead
// of the regional host, in the SDK's own precedence: the service-specific
// variable wins over the global one. A Secret that sets either is one whose
// runs reach Bedrock through a PrivateLink or FIPS address, and the probe has
// to follow it to be probing the same thing.
func bedrockEndpointOverride(secret *corev1.Secret) string {
	for _, key := range []string{awsBedrockEndpointURLEnv, awsEndpointURLEnv} {
		if v := strings.TrimSpace(string(secret.Data[key])); v != "" {
			return v
		}
	}
	return ""
}

// bedrockProbeRegion is the Secret's AWS_REGION, trimmed. This is the region
// the probe signs with and, absent an override, the region whose host it
// calls - the same single source of truth the run uses.
func bedrockProbeRegion(secret *corev1.Secret) string {
	return strings.TrimSpace(string(secret.Data[awsRegionEnv]))
}

// bedrockEndpointVariantKeys are the settings that move the endpoint away from
// the plain regional host. The probe does not reproduce them; it declines to
// probe instead (see gatewayProbeSkip).
var bedrockEndpointVariantKeys = []string{
	awsUseFIPSEndpointEnv,
	awsUseDualStackEndpointEnv,
}

// setBedrockEndpointVariants returns the endpoint-variant settings the Secret
// turns on. Anything other than unset, empty or an explicit false counts as
// on: the consequence of reading this wrong is declining to probe, which is
// the safe direction, where guessing the variant's host is not.
func setBedrockEndpointVariants(secret *corev1.Secret) []string {
	var set []string
	for _, key := range bedrockEndpointVariantKeys {
		switch strings.ToLower(strings.TrimSpace(string(secret.Data[key]))) {
		case "", "false", "0":
		default:
			set = append(set, key)
		}
	}
	return set
}

// redactEndpoint returns an endpoint safe to put on a Gateway's status, which
// is readable by anyone who can read the Gateway. An endpoint can carry
// credentials in two places - userinfo and query values - and the shapes most
// likely to do so are exactly the ones the probe declines, whose skip message
// quotes the endpoint back. Keys are kept because they are useful for
// diagnosis and are not the secret; values are not.
//
// A value that will not parse as a URL is returned unchanged: it reached here
// from a Secret or a spec field, it is not a URL, and losing it entirely would
// make the message useless.
func redactEndpoint(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	if parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" {
		return endpoint
	}
	if parsed.User != nil {
		parsed.User = url.User(redactedPlaceholder)
	}
	if parsed.RawQuery != "" {
		query := parsed.Query()
		for key := range query {
			query[key] = []string{redactedPlaceholder}
		}
		parsed.RawQuery = query.Encode()
	}
	if parsed.Fragment != "" {
		parsed.Fragment = redactedPlaceholder
	}
	return parsed.String()
}

// isSignableEndpointOverride reports whether an endpoint override is a shape
// the signer can reproduce exactly. A base path is fine - it becomes part of
// the signed path - but a query, a fragment or embedded credentials are shapes
// the canonical request would have to model and this signer does not, so they
// are declined rather than probed with a signature that cannot match.
func isSignableEndpointOverride(override string) bool {
	parsed, err := url.Parse(override)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return false
	}
	return parsed.RawQuery == "" && parsed.Fragment == "" && parsed.User == nil
}

// isAWSRegionName reports whether s has the shape of an AWS region name
// (us-east-1, cn-north-1, us-gov-west-1). The probe puts this value in a URL
// host and in the SigV4 credential scope, so it is checked rather than
// trusted: anything else is skipped, not signed for.
func isAWSRegionName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' && i > 0 && i < len(s)-1:
		default:
			return false
		}
	}
	return true
}

// isHTTPSURL reports whether s parses as an https URL with a host. The probe
// signs the request it sends, so plain http would put a usable signature -
// and the session token it forwards - on the wire in the clear.
func isHTTPSURL(s string) bool {
	parsed, err := url.Parse(s)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != ""
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

// awsEscapePathSegment percent-encodes s for use as a single path label,
// following AWS's own UriEncode: every byte except the RFC 3986 unreserved
// characters is escaped, with uppercase hex digits.
//
// A Bedrock modelId is one path label, and it can be an inference-profile ARN
// (arn:aws:bedrock:<region>:<account>:inference-profile/<id>). Its slashes must
// not become path separators: raw, bedrock-runtime answers HTTP 200 carrying a
// coral UnknownOperationException instead of routing to ConverseStream, so the
// probe would record a Gateway that cannot work as verified.
//
// The escaped form is what goes on the wire. SigV4's canonical URI is that
// path encoded once more (awsEscapeURIPath), which is why the controller signs
// the request itself rather than letting curl do it - see
// signBedrockProbeRequest.
func awsEscapePathSegment(s string) string {
	const unreserved = "-._~"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			strings.IndexByte(unreserved, c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// awsEscapeURIPath percent-encodes each segment of path while keeping the
// separators, which is SigV4's canonical URI for every service but S3: the
// path as sent, URI-encoded once more.
func awsEscapeURIPath(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = awsEscapePathSegment(segment)
	}
	return strings.Join(segments, "/")
}

// bedrockProbeRequest is the signed ConverseStream request the probe performs.
// The controller builds and signs it; the pod only replays it.
type bedrockProbeRequest struct {
	// URL is the full request URL, model id already escaped as one segment.
	URL string
	// Authorization is the SigV4 header, which carries the access key id and
	// the signature but never the secret access key.
	Authorization string
	// AMZDate is the x-amz-date the signature covers.
	AMZDate string
	// SecurityToken is the Secret's AWS_SESSION_TOKEN when it has one. It is
	// a signed header, so a temporary credential cannot be probed without it.
	SecurityToken string
}

// signBedrockProbeRequest signs the probe's ConverseStream call with SigV4,
// in the controller, using only crypto/hmac and crypto/sha256.
//
// The controller signs rather than curl because curl 8.12.1 - the version
// ubi10/ubi-minimal ships, which is the verification image's base - gets the
// canonical URI wrong. SigV4 requires the canonical path to be the path as
// sent, URI-encoded once more (every service but S3). curl 8.12.1 encodes a
// raw path once and then signs that, leaving any percent-escape already in the
// URL untouched, so:
//
//   - send the raw colon in a model id, and curl signs "%3A" where AWS also
//     expects "%3A" - those agree, which is why this probe appeared to work;
//   - send "%3A", and curl signs "%3A" where AWS expects "%253A" - they
//     disagree, and AWS answers SignatureDoesNotMatch.
//
// The second is the shape this probe needs, because an inference-profile ARN
// has to arrive percent-escaped or bedrock-runtime does not route it at all
// (see awsEscapePathSegment). So there is no spelling of the URL that both
// routes and verifies on this curl. The behaviour is fixed in curl 8.14.1,
// which the image does not have and this change deliberately does not require.
//
// Signing here also keeps the secret access key out of the probe pod: it never
// leaves the controller. What the pod does receive is the access key id and
// signature inside the Authorization header, and the session token when the
// credential is temporary - enough to replay this one request until the
// signature ages out, not enough to sign another.
//
// The signature is good for AWS's clock-skew window of about five minutes. The
// verification Job's ActiveDeadlineSeconds (verificationDeadline, 120s) bounds
// how long it can sit unused, so a slow image pull fails the Job on its own
// deadline rather than producing an expired signature; a controller whose clock
// is wrong is the remaining case, and it is classified as such rather than
// reported as a bad credential (see bedrockSkewBodyPattern).
func signBedrockProbeRequest(
	secret *corev1.Secret,
	probeEndpoint, modelID string,
	now time.Time,
) (bedrockProbeRequest, error) {
	region := bedrockProbeRegion(secret)
	keyID := strings.TrimSpace(string(secret.Data[awsAccessKeyIDEnv]))
	secretKey := strings.TrimSpace(string(secret.Data[awsSecretAccessKeyEnv]))
	token := strings.TrimSpace(string(secret.Data[awsSessionTokenEnv]))

	parsed, err := url.Parse(probeEndpoint)
	if err != nil {
		return bedrockProbeRequest{}, fmt.Errorf("parsing probe endpoint: %w", err)
	}
	host := parsed.Host
	if host == "" {
		return bedrockProbeRequest{}, fmt.Errorf("probe endpoint %q has no host", probeEndpoint)
	}

	// Build the request URL first, then take the canonical path from it, so
	// the signature always covers the path actually sent. An endpoint override
	// may carry a base path (a proxy at https://proxy.example/bedrock sends
	// /bedrock/model/...); signing /model/... alone produced a signature AWS
	// could not match. parsed.EscapedPath keeps the override's own escaping,
	// since the canonical form is that path encoded once more.
	basePath := strings.TrimSuffix(parsed.EscapedPath(), "/")
	wirePath := basePath + "/model/" + awsEscapePathSegment(modelID) +
		"/" + bedrockConverseStreamOperation
	canonicalPath := awsEscapeURIPath(wirePath)

	amzDate := now.UTC().Format("20060102T150405Z")
	dateStamp := amzDate[:8]
	scope := dateStamp + "/" + region + "/" + bedrockSigningService + "/aws4_request"

	// Canonical headers, lowercase and sorted. content-type is signed because
	// the probe sends it; host because SigV4 always requires it.
	headers := []struct{ name, value string }{
		{"content-type", "application/json"},
		{"host", host},
		{"x-amz-date", amzDate},
	}
	if token != "" {
		headers = append(headers, struct{ name, value string }{"x-amz-security-token", token})
	}
	var canonicalHeaders strings.Builder
	signedNames := make([]string, 0, len(headers))
	for _, h := range headers {
		canonicalHeaders.WriteString(h.name + ":" + h.value + "\n")
		signedNames = append(signedNames, h.name)
	}
	signedHeaders := strings.Join(signedNames, ";")

	payloadHash := sha256.Sum256([]byte(bedrockProbeBody))
	canonicalRequest := strings.Join([]string{
		"POST",
		canonicalPath,
		"", // no query string
		canonicalHeaders.String(),
		signedHeaders,
		hex.EncodeToString(payloadHash[:]),
	}, "\n")

	canonicalHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		awsSigV4Algorithm,
		amzDate,
		scope,
		hex.EncodeToString(canonicalHash[:]),
	}, "\n")

	signingKey := []byte("AWS4" + secretKey)
	for _, part := range []string{dateStamp, region, bedrockSigningService, "aws4_request"} {
		signingKey = hmacSHA256(signingKey, part)
	}
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	return bedrockProbeRequest{
		// Rebuilt from the parsed parts rather than concatenated onto the
		// override string, so the URL and the signed path cannot drift: the
		// base path is already in wirePath.
		URL: parsed.Scheme + "://" + host + wirePath,
		Authorization: fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
			awsSigV4Algorithm, keyID, scope, signedHeaders, signature),
		AMZDate:       amzDate,
		SecurityToken: token,
	}, nil
}

// hmacSHA256 is one round of the SigV4 signing-key derivation.
func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// gatewayVerificationBedrockCommand builds the shell command for the
// aws-bedrock probe: the ConverseStream request the controller already signed
// (see signBedrockProbeRequest), replayed with curl. The pod does no signing,
// so the probe is correct on any curl version.
//
// The diagnostic vocabulary is the models probe's, plus the provider's own
// error body as a free-text detail= tail (see diagDetail):
//   - "ok code=<n>"                      a 2xx - the credential invoked the model
//   - "auth code=<n> detail=<body>"      401/403 - rejected or denied by IAM/SCP
//   - "http code=<n> detail=<body>"      any other non-2xx
//   - "errorbody code=<n> detail=<body>" a 2xx carrying an AWS error, which
//     means the request never reached the model (see bedrockErrorBodyPattern)
//   - "skew code=<n> detail=<body>"      403 because the signature has aged
//     out or the controller's clock is wrong, not because the credential is
//     bad (see bedrockSkewBodyPattern)
//   - "unreachable rc=<n>"               curl transport error
//
// Carrying the body is the point of the whole change: an SCP denial answers
// 403 with the operation it refused ("not authorized to perform:
// bedrock:InvokeModelWithResponseStream on ..."), and that sentence is what
// the Gateway status should say. The body names an IAM principal, never the
// secret - and nothing is written anywhere but the termination log.
//
// Nothing is interpolated into the command string: the signed URL, the
// Authorization header and the session token all arrive as env vars, and the
// secret access key never reaches the pod at all.
func gatewayVerificationBedrockCommand() string {
	// A session token is part of the credential, not a header curl can be
	// handed empty: an empty x-amz-security-token would be sent and rejected,
	// and it is a signed header, so it is added only when the controller
	// signed one in.
	curl := `curl -s --max-time 10` +
		` -H "content-type: application/json"` +
		` -H "x-amz-date: $` + envSigV4Date + `"` +
		` -H "Authorization: $` + envSigV4Authorization + `"` +
		` -d '` + bedrockProbeBody + `'` +
		` -w '\n%{http_code}' "$@" "$` + envProbeURL + `"`

	return `set --
if [ -n "$` + envSigV4SecurityToken + `" ]; then set -- -H "x-amz-security-token: $` + envSigV4SecurityToken + `"; fi
resp=$(` + curl + `); rc=$?
if [ "$rc" -ne 0 ]; then echo "unreachable rc=$rc" > /dev/termination-log; exit 1; fi
code=$(printf '%s' "$resp" | tail -n1)
body=$(printf '%s' "$resp" | sed '$d')
detail=$(printf '%s' "$body" | tr -d '\r' | tr '\n' ' ' | cut -c1-` + strconv.Itoa(verificationDetailLimit) + `)
if echo "$code" | grep -qE '` + verificationHTTPCodePattern + `'; then
  if printf '%s' "$body" | grep -qE '` + bedrockErrorBodyPattern + `'; then
    printf 'errorbody code=%s detail=%s\n' "$code" "$detail" > /dev/termination-log; exit 1
  fi
  echo "ok code=$code" > /dev/termination-log; exit 0
fi
case "$code" in
401|403)
  if printf '%s' "$body" | grep -qE '` + bedrockSkewBodyPattern + `'; then
    printf 'skew code=%s detail=%s\n' "$code" "$detail" > /dev/termination-log; exit 1
  fi
  printf 'auth code=%s detail=%s\n' "$code" "$detail" > /dev/termination-log ;;
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
// gateway endpoint using the agent base image. probeEndpoint is the address
// the probe calls: spec.endpoint for every provider but Bedrock, whose
// endpoint comes from the credential Secret instead (bedrockProbeEndpoint).
// signed carries the controller-signed Bedrock request and is the zero value
// for every other probe.
func (r *GatewayReconciler) createVerificationJob(
	ctx context.Context,
	gateway *konveyoriov1alpha1.Gateway,
	jobName string,
	probeEndpoint string,
	signed bedrockProbeRequest,
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
	env := []corev1.EnvVar{{Name: envLLMEndpoint, Value: probeEndpoint}}
	var envFrom []corev1.EnvFromSource
	switch {
	case probe == probeBedrockConverse:
		// The controller signed the request (signBedrockProbeRequest), so the
		// pod gets the signed URL, the Authorization header and the date -
		// and, only for a temporary credential, the session token the
		// signature covers. The secret access key stays in the controller.
		//
		// The Secret is deliberately not mounted via envFrom any more: the
		// probe has nothing left to sign with, so nothing to be handed.
		env = append(env,
			corev1.EnvVar{Name: envProbeURL, Value: signed.URL},
			corev1.EnvVar{Name: envSigV4Authorization, Value: signed.Authorization},
			corev1.EnvVar{Name: envSigV4Date, Value: signed.AMZDate},
		)
		if signed.SecurityToken != "" {
			env = append(env, corev1.EnvVar{
				Name:  envSigV4SecurityToken,
				Value: signed.SecurityToken,
			})
		}
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
		case "skew":
			// Not the credential: the signature aged out or the controller's
			// clock disagrees with AWS's. Say that, so nobody goes looking at
			// a Secret that is fine.
			if code != "" {
				return reasonConnectionFailed, fmt.Sprintf(
					"%s rejected the request's signature as expired (HTTP %s) - the "+
						"controller's clock is probably wrong, the credential is not "+
						"implicated%s",
					probeSurface(probe, endpoint), code, detail)
			}
		case "errorbody":
			// A 2xx is not a pass when the body is an AWS error: the request
			// did not reach the model. Said plainly, because "returned HTTP
			// 200" on a failed verification reads like a contradiction.
			if code != "" {
				return reasonConnectionFailed, fmt.Sprintf(
					"%s answered HTTP %s with an AWS error body, so the request "+
						"never reached the model%s",
					probeSurface(probe, endpoint), code, detail)
			}
		case "unreachable":
			return reasonEndpointUnreachable, fmt.Sprintf(
				"Endpoint %s is unreachable (%s)",
				redactEndpoint(endpoint), curlErrorPhrase(diagValue(fields, "rc")))
		}
	}

	return reasonConnectionFailed, fmt.Sprintf("Verification Job %q failed", jobName)
}

// probeSurface names what the probe actually called, so a failure message
// points at the request that produced it rather than at the bare endpoint.
func probeSurface(probe gatewayProbeKind, endpoint string) string {
	endpoint = redactEndpoint(endpoint)
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
	endpoint = redactEndpoint(endpoint)
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
