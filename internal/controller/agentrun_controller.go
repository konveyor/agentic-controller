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
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"

	"github.com/konveyor/agentic-controller/api/skill"
	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

const (
	// secretKeyLength is the length of the generated ACP secret key in bytes.
	secretKeyLength = 32

	// acpPort is the port every agent image serves ACP on inside the sandbox
	// pod (the harness tee, or goose serve directly). Clients reach it as
	// <sandbox>.<namespace>.svc:4000.
	acpPort     int32 = 4000
	acpPortName       = "acp"

	// acpProbePeriodSeconds paces the ACP readiness probe. Readiness only
	// gates the pod's Ready condition (and so phase=Running); it never
	// restarts the container, so a run that dies before listening still
	// ends through the pod's terminal phase.
	acpProbePeriodSeconds int32 = 2

	// workspaceVolumeName is the name of the EmptyDir volume for the agent workspace.
	workspaceVolumeName = "workspace"

	tmpVolumeName = "tmp"

	// skillsVolumeName backs the assembled skills root. The loader writes it,
	// the agent reads it.
	skillsVolumeName = "skills"

	// skillsDir is the mount contract from ADR 0001: every skill lives at
	// /opt/skills/{name}/SKILL.md, one directory, whatever delivered it.
	skillsDir = "/opt/skills"

	// skillsSrcDir stages each source read-only. The loader assembles skillsDir
	// from what it finds here, which is what lets one image carry several
	// skills without widening the mount contract.
	skillsSrcDir = "/opt/skills-src"

	// skillLoaderContainerName is the init container that assembles and
	// validates the skills root before the agent starts.
	skillLoaderContainerName = "skill-loader"

	// loaderBinary is the skill loader in the controller's own image. An
	// absolute path because that image is distroless and has no PATH.
	// Named explicitly rather than left to the image's ENTRYPOINT: an agent
	// image is free to wrap its entrypoint in a script, and the loader has to
	// run the subcommand either way.
	loaderBinary = "/skill-loader"

	// agentContainerName is the container that runs the agent itself.
	agentContainerName = "agent"

	// skillFileKey is the ConfigMap key an inline skill's markdown lands under,
	// and the filename the loader expects to find once mounted.
	skillFileKey = skill.File

	// skillSourcesEnv declares every skill source to the loader as JSON:
	// which are staged, which must be cloned, and any load policy the
	// SkillCard imposes on what they carry.
	skillSourcesEnv = "KONVEYOR_SKILL_SOURCES"

	// gitAuthorNameEnv / gitAuthorEmailEnv carry the resolved commit
	// identity to the harness. They are controller-managed: a user copy in
	// run.Spec.Env is dropped, and the controller always emits both (even
	// empty) so container env outranks any copy smuggled in through
	// run.Spec.EnvFrom. Neither path can bypass GitConfig's validation
	// (the both-or-neither and no-metacharacter rules that guard against
	// forged or half-set commit authorship).
	gitAuthorNameEnv  = "KONVEYOR_GIT_AUTHOR_NAME"
	gitAuthorEmailEnv = "KONVEYOR_GIT_AUTHOR_EMAIL"

	// sandboxConditionFinished is the Sandbox condition type that reports the
	// run has reached a terminal state (Succeeded or Failed).
	sandboxConditionFinished = "Finished"

	// paramsVolumeName is the name of the ConfigMap volume carrying
	// /run/konveyor/params.json (ADR 0009).
	paramsVolumeName = "konveyor-params"

	// sandboxFinishedReasonSucceeded is the Sandbox condition reason for
	// success. Must match Agent Sandbox's SandboxReasonPodSucceeded constant.
	sandboxFinishedReasonSucceeded = "PodSucceeded"

	// agentRunRefIndexField is the field index for looking up AgentRuns by agentRef.
	agentRunRefIndexField = ".spec.agentRef"
)

// errGatewayNotFound marks a createSandbox failure caused by the run's selected
// Gateway CR not existing (as opposed to existing-but-not-Ready, or any other
// createSandbox error). The caller uses it to report a distinct GatewayNotFound
// status reason and retry with backoff, ImagePullBackOff-style: a missing
// Gateway is treated as a timing condition that may resolve on its own (the CR
// is applied moments later, or the informer cache catches up), not a terminal
// failure that would strand a run whose spec is immutable.
var errGatewayNotFound = stderrors.New("selected gateway does not exist")

// AgentRunReconciler reconciles an AgentRun object.
type AgentRunReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// SkillLoaderImage runs the skill-loader init container. It is the
	// controller's own image, so an agent image is not required to carry the
	// harness binary and the source list written here is always read by a
	// loader of the same version.
	SkillLoaderImage string

	// DefaultTTLAfterFinished, when non-nil, is the fallback lifetime applied
	// to a terminal AgentRun whose spec does not set TTLSecondsAfterFinished.
	// The controller deletes such a run this long after it finished. Nil
	// disables the default (runs are kept until deleted). A run's own
	// spec.ttlSecondsAfterFinished always overrides this. Wired from the
	// --agentrun-ttl flag in main.
	DefaultTTLAfterFinished *time.Duration

	// DefaultStartupDeadline bounds how long a run may take to reach a
	// running state before it is failed with StartupDeadlineExceeded, for
	// runs that do not set spec.startupDeadlineSeconds. Nil or zero means
	// no default deadline; fatal pod errors still fail runs immediately.
	// Set from --agentrun-startup-deadline.
	DefaultStartupDeadline *time.Duration

	// apiReader reads directly from the API server, bypassing the manager
	// cache. It is used only for the best-effort pod termination-message
	// lookup: a direct read avoids surfacing a stale generic message when
	// the label-restricted Pod cache (see SandboxPodCacheOptions) lags the
	// Sandbox "Finished" condition. The cache-backed client still serves the
	// phase=Running read, so the controller keeps watching sandbox pods. Set
	// by SetupWithManager.
	apiReader client.Reader
}

// +kubebuilder:rbac:groups=konveyor.io,resources=agentruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=konveyor.io,resources=agentruns/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=konveyor.io,resources=agentruns/finalizers,verbs=update
// +kubebuilder:rbac:groups=agents.x-k8s.io,resources=sandboxes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;delete

// Reconcile handles AgentRun reconciliation.
//
// The controller:
// 1. Checks that the referenced Agent exists and is Ready
// 2. Validates params and gateway selection against Agent declarations
// 3. Resolves skills to OCI image refs (fails if any are unresolvable)
// 4. Creates a Sandbox CR with the agent image, skills, env, and workspace
// 5. Watches the Sandbox to completion and updates AgentRun status
func (r *AgentRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var run konveyoriov1alpha1.AgentRun
	if err := r.Get(ctx, req.NamespacedName, &run); err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		// Owner references are the primary cascade. The label-driven sweep is
		// the recovery path for a child whose owner reference was removed or
		// never persisted. Confirm against the uncached API before deleting:
		// child and parent informer events are not ordered across resource
		// types, so a cache miss alone is not a safe deletion signal.
		if r.apiReader != nil {
			var current konveyoriov1alpha1.AgentRun
			if err := r.apiReader.Get(ctx, req.NamespacedName, &current); err == nil {
				return ctrl.Result{Requeue: true}, nil
			} else if !errors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, r.cleanupOrphanedRunResources(ctx, req.NamespacedName)
	}

	logger.V(1).Info("Reconciling AgentRun", "name", run.Name)

	original := run.DeepCopy()
	run.Status.ObservedGeneration = run.Generation

	// A terminal run has no execution work left; the only remaining action is
	// TTL-based garbage collection (delete once the run's lifetime elapses).
	if isTerminalPhase(run.Status.Phase) {
		return r.reconcileTTL(ctx, &run, original)
	}

	// Shed the legacy Ready condition on any live run created before it was
	// removed from AgentRun (ADR 0018); the terminal outcome now lives on
	// Succeeded, serving on ACPReady. Runs clean up on their next reconcile.
	meta.RemoveStatusCondition(&run.Status.Conditions, ConditionTypeReady)

	// Look up the referenced Agent.
	var agent konveyoriov1alpha1.Agent
	agentKey := types.NamespacedName{Namespace: run.Namespace, Name: run.Spec.AgentRef}
	if err := r.Get(ctx, agentKey, &agent); err != nil {
		if errors.IsNotFound(err) {
			run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
			setRunSucceeded(&run, metav1.ConditionFalse, "AgentNotFound",
				fmt.Sprintf("Agent %q not found", run.Spec.AgentRef))
			return r.patchRunStatus(ctx, &run, original)
		}
		return ctrl.Result{}, err
	}

	// Check that the Agent is Ready before proceeding. The run is not
	// failed — it waits — so Succeeded stays Unknown.
	agentReady := meta.FindStatusCondition(agent.Status.Conditions, ConditionTypeReady)
	if agentReady == nil || agentReady.Status != metav1.ConditionTrue {
		setRunSucceeded(&run, metav1.ConditionUnknown, "AgentNotReady",
			fmt.Sprintf("Agent %q is not Ready", run.Spec.AgentRef))
		return r.patchRunStatus(ctx, &run, original)
	}

	// If no Sandbox exists yet, validate config against the Agent and
	// create it. Validation runs only here, not on every reconcile of a
	// live run: the Sandbox bakes in the rendered prompt/params at
	// creation, so re-validating a running run against a since-edited
	// Agent would change nothing except to spuriously fail it. (Immunity
	// of not-yet-created workflow stages to Agent edits is tracked
	// separately in #180.)
	if run.Status.SandboxName == "" {
		// Validate params against Agent declarations; the resolved params
		// and substitution scopes feed createSandbox.
		params, scopes, err := r.validateParams(&run, &agent)
		if err != nil {
			run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
			setRunSucceeded(&run, metav1.ConditionFalse, "InvalidParams", err.Error())
			return r.patchRunStatus(ctx, &run, original)
		}

		// Validate gateway selection against Agent's available gateways.
		if err := r.validateGateway(&run, &agent); err != nil {
			run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
			setRunSucceeded(&run, metav1.ConditionFalse, "InvalidGateway", err.Error())
			return r.patchRunStatus(ctx, &run, original)
		}

		sandboxName, err := r.createSandbox(ctx, &run, &agent, params, scopes)
		if err != nil {
			// A named Gateway that does not exist yet is treated like a
			// missing container image (ImagePullBackOff): retriable, not
			// terminal. It may be created moments after the run (a bundled
			// GitOps apply of Gateway+Agent+AgentRun), or simply be lagging
			// the informer cache. Failing terminally would strand a run whose
			// spec is immutable -- the user could only delete and recreate to
			// recover from what is really a timing issue. We surface a distinct
			// GatewayNotFound reason so the wait is legible, then requeue with
			// the same exponential backoff every transient createSandbox error
			// uses.
			reason := "SandboxCreationFailed"
			msg := fmt.Sprintf("Failed to create Sandbox for Agent %q: %v", agent.Name, err)
			if stderrors.Is(err, errGatewayNotFound) {
				reason = "GatewayNotFound"
				msg = fmt.Sprintf("Gateway %q not found; waiting for it to be created", run.Spec.Gateway)
				logger.Info("AgentRun references a Gateway that does not exist yet; will retry", "agentRun", run.Name, "gateway", run.Spec.Gateway)
			} else {
				logger.Error(err, "Failed to create Sandbox", "agentRun", run.Name, "agent", agent.Name)
			}
			// Transient — the reconcile requeues with backoff — so the
			// run is not yet failed; Succeeded stays Unknown.
			setRunSucceeded(&run, metav1.ConditionUnknown, reason, msg)
			// Patch status then return the error so controller-runtime
			// requeues with exponential backoff.
			if _, patchErr := r.patchRunStatus(ctx, &run, original); patchErr != nil {
				return ctrl.Result{}, patchErr
			}
			return ctrl.Result{}, err
		}
		run.Status.SandboxName = sandboxName
		run.Status.Phase = konveyoriov1alpha1.AgentRunPhasePending
		setRunSucceeded(&run, metav1.ConditionUnknown, "SandboxCreated",
			fmt.Sprintf("Sandbox %q created", sandboxName))
		return r.patchRunStatus(ctx, &run, original)
	}

	// Watch the Sandbox status.
	var sandbox sandboxv1beta1.Sandbox
	sandboxKey := types.NamespacedName{Namespace: run.Namespace, Name: run.Status.SandboxName}
	if err := r.Get(ctx, sandboxKey, &sandbox); err != nil {
		if errors.IsNotFound(err) {
			run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
			setRunSucceeded(&run, metav1.ConditionFalse, "SandboxNotFound",
				fmt.Sprintf("Sandbox %q was deleted", run.Status.SandboxName))
			return r.patchRunStatus(ctx, &run, original)
		}
		return ctrl.Result{}, err
	}

	// The sandbox pod (Agent Sandbox names it after the Sandbox) tells us
	// whether the agent process is executing; its absence just means "not
	// yet".
	var pod *corev1.Pod
	var p corev1.Pod
	if err := r.Get(ctx, sandboxKey, &p); err == nil {
		pod = &p
	} else if !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	// Update AgentRun phase and ACP readiness from the Sandbox and its pod.
	// A non-zero requeue enforces the startup deadline without relying on a
	// pod event that may never come (e.g. a pod stuck unschedulable).
	requeueAfter := r.updatePhaseFromSandbox(ctx, &run, &sandbox, pod)

	if _, err := r.patchRunStatus(ctx, &run, original); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// validateParams checks that supplied params match Agent declarations
// and returns the resolved params.json content and substitution scopes
// for createSandbox to reuse.
func (r *AgentRunReconciler) validateParams(
	run *konveyoriov1alpha1.AgentRun,
	agent *konveyoriov1alpha1.Agent,
) (paramsFile, map[string]map[string]string, error) {
	// Build a map of declared params.
	declared := make(map[string]konveyoriov1alpha1.Param)
	for _, p := range agent.Spec.Params {
		declared[p.Name] = p
	}

	// Check that all supplied params are declared.
	for _, p := range run.Spec.Params {
		if _, ok := declared[p.Name]; !ok {
			return paramsFile{}, nil, fmt.Errorf("param %q is not declared by Agent %q", p.Name, agent.Name)
		}
	}

	// Check that all required params (without defaults) are supplied.
	supplied := make(map[string]bool)
	for _, p := range run.Spec.Params {
		supplied[p.Name] = true
	}
	for _, p := range agent.Spec.Params {
		if p.Required && p.Default == "" && !supplied[p.Name] {
			return paramsFile{}, nil, fmt.Errorf("required param %q not supplied", p.Name)
		}
	}

	// Resolve params (type coercion) and validate prompt/instruction
	// substitution up front. These are permanent config errors (a
	// non-numeric value for a number param, a reference to an undeclared
	// param), so surfacing them here fails the run terminally with
	// InvalidParams rather than requeuing forever as
	// SandboxCreationFailed. The resolved params and scopes are returned
	// so createSandbox reuses them instead of recomputing.
	params, scopes, err := buildParams(run, agent)
	if err != nil {
		return paramsFile{}, nil, err
	}
	if _, _, err := renderPromptAndInstructions(agent, run, scopes); err != nil {
		return paramsFile{}, nil, err
	}

	return params, scopes, nil
}

// renderPromptAndInstructions substitutes $(scope.name) references in the
// Agent prompt and the AgentRun instructions. It is the single place both
// the up-front validation and the env-var construction resolve these two
// fields, so a reference that passes validation renders identically at
// sandbox creation.
func renderPromptAndInstructions(
	agent *konveyoriov1alpha1.Agent,
	run *konveyoriov1alpha1.AgentRun,
	scopes map[string]map[string]string,
) (prompt, instructions string, err error) {
	prompt, err = substitute(agent.Spec.Prompt, scopes)
	if err != nil {
		return "", "", fmt.Errorf("prompt: %w", err)
	}
	instructions, err = substitute(run.Spec.Instructions, scopes)
	if err != nil {
		return "", "", fmt.Errorf("instructions: %w", err)
	}
	return prompt, instructions, nil
}

// validateGateway resolves and validates the gateway for a run against the
// Agent's declared gateway set, which is a presence-gated curation constraint:
//   - Run names a gateway + Agent list non-empty: enforce membership (the
//     architect has locked the Agent to specific gateways).
//   - Run names a gateway + Agent list empty: accept it unconstrained. The
//     Gateway CR must still exist and be Ready, which createSandbox enforces.
//   - Run omits a gateway + Agent declares exactly one: default to it.
//   - Run omits a gateway + Agent declares zero or multiple: error. The
//     zero and multiple cases get distinct messages because the fix differs —
//     name any gateway vs. pick one of the declared set.
//
// The Agent controller already watches Gateway CRs and won't report Ready if a
// declared Gateway is missing, so dangling references in a non-empty list are
// caught upstream by the "Agent not Ready" check.
func (r *AgentRunReconciler) validateGateway(
	run *konveyoriov1alpha1.AgentRun,
	agent *konveyoriov1alpha1.Agent,
) error {
	if run.Spec.Gateway == "" {
		// Default to the Agent's only gateway when exactly one is
		// declared. Otherwise require explicit selection so the run fails
		// fast instead of dying at runtime on a missing gateway.
		switch len(agent.Spec.Gateways) {
		case 1:
			run.Spec.Gateway = agent.Spec.Gateways[0].Ref
			return nil
		case 0:
			return fmt.Errorf("agent %q declares no gateways; select one via spec.gateway",
				agent.Name)
		default:
			return fmt.Errorf("agent %q declares %d gateways; select one via spec.gateway",
				agent.Name, len(agent.Spec.Gateways))
		}
	}
	// An empty Agent gateway list constrains nothing: accept the run's
	// selection as-is. createSandbox verifies the Gateway CR exists and is
	// Ready before it is used.
	if len(agent.Spec.Gateways) == 0 {
		return nil
	}
	for _, g := range agent.Spec.Gateways {
		if g.Ref == run.Spec.Gateway {
			return nil
		}
	}
	return fmt.Errorf("gateway %q is not in Agent %q gateways", run.Spec.Gateway, agent.Name)
}

// createSandbox creates the Sandbox CR, the ACP secret key Secret,
// and returns the Sandbox name.
func (r *AgentRunReconciler) createSandbox(
	ctx context.Context,
	run *konveyoriov1alpha1.AgentRun,
	agent *konveyoriov1alpha1.Agent,
	params paramsFile,
	scopes map[string]map[string]string,
) (string, error) {
	sandboxName := run.Name

	// Generate ACP secret key.
	secretKey, err := generateSecretKey()
	if err != nil {
		return "", fmt.Errorf("generating secret key: %w", err)
	}

	// Create the Secret for the ACP key.
	secretName := sandboxName + "-acp-key"
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: run.Namespace,
			Labels: map[string]string{
				labelManagedBy: managedByLabel,
				labelAgentRun:  run.Name,
			},
		},
		StringData: map[string]string{
			"secret-key": secretKey,
		},
	}
	if err := ctrl.SetControllerReference(run, secret, r.Scheme); err != nil {
		return "", fmt.Errorf("setting Secret owner reference: %w", err)
	}
	if err := r.Create(ctx, secret); err != nil && !errors.IsAlreadyExists(err) {
		return "", fmt.Errorf("creating ACP Secret: %w", err)
	}

	// Update the run status with the secret ref.
	run.Status.SecretKeyRef = &corev1.LocalObjectReference{Name: secretName}

	// params and scopes were resolved and validated by validateParams
	// (ADR 0009/0018) and passed in, so a coercion or unresolved-reference
	// failure has already aborted the run terminally before this point.

	// Build env vars: ACP secret key + LLM credentials + substituted
	// prompt/instructions. Params ride params.json, not env.
	env, envFrom, err := r.buildEnvVars(ctx, run, agent, secretName, scopes)
	if err != nil {
		return "", fmt.Errorf("building env vars: %w", err)
	}

	// Create the params.json ConfigMap and mount it at
	// /run/konveyor/params.json.
	paramsVolume, paramsMount, err := r.createParamsConfigMap(ctx, run, params)
	if err != nil {
		return "", fmt.Errorf("creating params ConfigMap: %w", err)
	}

	// Stage skill sources. Images become ImageVolumes, inline content becomes a
	// ConfigMap, git sources are handed to the loader. All are read-only under
	// /opt/skills-src, none of them built here.
	skillSrc, err := r.resolveSkillVolumes(ctx, run, agent)
	if err != nil {
		return "", fmt.Errorf("resolving skill volumes: %w", err)
	}
	if err := r.createInlineSkillConfigMaps(ctx, run, skillSrc.inline); err != nil {
		return "", err
	}
	volumes := skillSrc.volumes
	// Staged sources are only visible to the loader. The agent sees the
	// assembled root and nothing else, so ADR 0001's one-directory contract
	// holds from the agent's side no matter how the skills arrived.
	loaderMounts := skillSrc.mounts

	// The assembled skills root: written by the loader, read by the agent.
	// Bounded like the workspace and /tmp below, because what lands here is
	// copied out of whatever image or repository a SkillCard names, and an
	// unbounded EmptyDir fills the node rather than failing the pod.
	volumes = append(volumes, corev1.Volume{
		Name: skillsVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{
				SizeLimit: resource.NewQuantity(1*1024*1024*1024, resource.BinarySI), // 1Gi
			},
		},
	})
	skillsMount := corev1.VolumeMount{Name: skillsVolumeName, MountPath: skillsDir}
	loaderMounts = append(loaderMounts, skillsMount)

	volumeMounts := make([]corev1.VolumeMount, 0, 4) // skills, params, workspace, tmp
	volumeMounts = append(volumeMounts, corev1.VolumeMount{
		Name:      skillsVolumeName,
		MountPath: skillsDir,
		ReadOnly:  true,
	})

	// params.json is mounted into the agent container only, not the loader.
	volumes = append(volumes, paramsVolume)
	volumeMounts = append(volumeMounts, paramsMount)

	// Add workspace EmptyDir.
	volumes = append(volumes, corev1.Volume{
		Name: workspaceVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{
				SizeLimit: resource.NewQuantity(10*1024*1024*1024, resource.BinarySI), // 10Gi
			},
		},
	})
	volumeMounts = append(volumeMounts, corev1.VolumeMount{
		Name:      workspaceVolumeName,
		MountPath: "/workspace",
	})

	// Writable /tmp for tools that create temp files at runtime.
	volumes = append(volumes, corev1.Volume{
		Name: tmpVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{
				SizeLimit: resource.NewQuantity(1*1024*1024*1024, resource.BinarySI), // 1Gi
			},
		},
	})
	volumeMounts = append(volumeMounts, corev1.VolumeMount{
		Name:      tmpVolumeName,
		MountPath: "/tmp",
	})

	// Create the Sandbox CR.
	serviceEnabled := true
	sandbox := &sandboxv1beta1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sandboxName,
			Namespace: run.Namespace,
			Labels: map[string]string{
				labelManagedBy: managedByLabel,
				labelComponent: "agent-sandbox",
				labelAgentRun:  run.Name,
				labelAgent:     agent.Name,
			},
		},
		Spec: sandboxv1beta1.SandboxSpec{
			SandboxBlueprint: sandboxv1beta1.SandboxBlueprint{
				PodTemplate: sandboxv1beta1.PodTemplate{
					// Agent Sandbox copies only PodTemplate metadata
					// onto the pod, so mirror the identifying labels here to
					// make the pod discoverable by AgentRun / Agent name.
					ObjectMeta: sandboxv1beta1.PodMetadata{
						Labels: map[string]string{
							labelManagedBy: managedByLabel,
							labelAgentRun:  run.Name,
							labelAgent:     agent.Name,
						},
					},
					Spec: corev1.PodSpec{
						// Never restart — a failed container must reach a terminal
						// phase so the AgentRun (and workflow stage) can observe
						// the failure. OnFailure would cause infinite crashloops
						// (#51). The tradeoff is that transient failures (image
						// pull blips, node eviction) are not retried. Bounded
						// retry (backoffLimit-style) can be added later if needed.
						RestartPolicy: corev1.RestartPolicyNever,
						InitContainers: []corev1.Container{
							skillLoaderContainer(r.SkillLoaderImage, skillSrc, loaderMounts),
						},
						Containers: []corev1.Container{
							{
								Name:  agentContainerName,
								Image: agent.Spec.Image,
								Env:   env,
								// User-specified sources last: for duplicate
								// keys across envFrom sources the last wins,
								// so run.spec.envFrom overrides provider
								// credentials.
								EnvFrom:      append(envFrom, run.Spec.EnvFrom...),
								VolumeMounts: volumeMounts,
								Ports: []corev1.ContainerPort{{
									Name:          acpPortName,
									ContainerPort: acpPort,
									Protocol:      corev1.ProtocolTCP,
								}},
								// The agent process binds the ACP port only once
								// it can serve (the harness starts goose, waits
								// for it, then listens), so an accepting socket
								// IS readiness. Without this probe the pod is
								// Ready the instant the process starts and
								// clients dial into a not-yet-listening port.
								ReadinessProbe: &corev1.Probe{
									ProbeHandler: corev1.ProbeHandler{
										TCPSocket: &corev1.TCPSocketAction{
											Port: intstr.FromInt32(acpPort),
										},
									},
									PeriodSeconds: acpProbePeriodSeconds,
								},
								// The harness writes a machine-readable failure
								// payload to the default termination-log path; the
								// controller lifts it onto AgentRunStatus. Fall back
								// to the last log lines if the harness dies before
								// writing (#143).
								TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
							},
						},
						Volumes: volumes,
					},
				},
				Service: &serviceEnabled,
			},
		},
	}

	if err := ctrl.SetControllerReference(run, sandbox, r.Scheme); err != nil {
		return "", fmt.Errorf("setting Sandbox owner reference: %w", err)
	}

	if err := r.Create(ctx, sandbox); err != nil && !errors.IsAlreadyExists(err) {
		return "", fmt.Errorf("creating Sandbox: %w", err)
	}

	return sandboxName, nil
}

// createParamsConfigMap renders params.json, creates (or leaves in
// place) a ConfigMap owned by the run, and returns the Volume and
// VolumeMount that surface it at /run/konveyor/params.json in the
// Sandbox. The ConfigMap is named <run>-params and is a non-secret
// vehicle (credentials ride the gateway envFrom path, not params.json).
func (r *AgentRunReconciler) createParamsConfigMap(
	ctx context.Context,
	run *konveyoriov1alpha1.AgentRun,
	params paramsFile,
) (corev1.Volume, corev1.VolumeMount, error) {
	content, err := renderParamsFile(params)
	if err != nil {
		return corev1.Volume{}, corev1.VolumeMount{}, fmt.Errorf("rendering params.json: %w", err)
	}

	// Derive the mount directory and ConfigMap key from the single
	// contract constant so the two cannot drift (ADR 0009).
	paramsDir := path.Dir(ParamsFilePath)  // /run/konveyor
	paramsKey := path.Base(ParamsFilePath) // params.json

	cmName := run.Name + "-params"
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name:      cmName,
		Namespace: run.Namespace,
	}}
	// CreateOrUpdate rather than a bare Create: createSandbox can fail and
	// retry, and the rendered content depends on the Agent's params and
	// execution, so an AlreadyExists swallow could leave a stale params.json
	// next to a freshly rendered prompt. Matches createInlineSkillConfigMaps.
	if _, err := ctrl.CreateOrUpdate(ctx, r.Client, cm, func() error {
		cm.Labels = map[string]string{
			labelManagedBy: managedByLabel,
			labelAgentRun:  run.Name,
		}
		cm.Data = map[string]string{paramsKey: string(content)}
		return ctrl.SetControllerReference(run, cm, r.Scheme)
	}); err != nil {
		return corev1.Volume{}, corev1.VolumeMount{}, fmt.Errorf("creating params ConfigMap: %w", err)
	}

	volume := corev1.Volume{
		Name: paramsVolumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
				Items: []corev1.KeyToPath{
					{Key: paramsKey, Path: paramsKey},
				},
			},
		},
	}
	mount := corev1.VolumeMount{
		Name:      paramsVolumeName,
		MountPath: paramsDir,
		ReadOnly:  true,
	}
	return volume, mount, nil
}

// buildEnvVars constructs the env var list for the Sandbox container, plus
// envFrom sources for the gateway's credential Secret when it is exposed
// whole (credentialRef without a key, e.g. AWS SigV4).
func (r *AgentRunReconciler) buildEnvVars(
	ctx context.Context,
	run *konveyoriov1alpha1.AgentRun,
	agent *konveyoriov1alpha1.Agent,
	acpSecretName string,
	scopes map[string]map[string]string,
) ([]corev1.EnvVar, []corev1.EnvFromSource, error) {
	var env []corev1.EnvVar
	var envFrom []corev1.EnvFromSource

	// Parameters are no longer injected as KONVEYOR_PARAM_* env vars.
	// They are delivered in /run/konveyor/params.json (ADR 0009) and
	// referenced in prompt text via $(agent.<name>) / $(workflow.<name>),
	// substituted by the controller below.

	// ACP secret key. The harness maps this to the runtime-specific
	// env var (e.g. GOOSE_SERVER__SECRET_KEY for Goose).
	env = append(env, corev1.EnvVar{
		Name: "KONVEYOR_ACP_SECRET_KEY",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: acpSecretName},
				Key:                  "secret-key",
			},
		},
	})

	// Prompt and instructions, with $(scope.name) substitution. Resolved
	// through the same helper the up-front validation uses so the two
	// paths cannot diverge.
	prompt, instructions, err := renderPromptAndInstructions(agent, run, scopes)
	if err != nil {
		return nil, nil, err
	}
	if run.Spec.Instructions != "" {
		env = append(env, corev1.EnvVar{
			Name:  "KONVEYOR_INSTRUCTIONS",
			Value: instructions,
		})
	}
	if agent.Spec.Prompt != "" {
		env = append(env, corev1.EnvVar{
			Name:  "KONVEYOR_PROMPT",
			Value: prompt,
		})
	}

	// Git commit identity. AgentRun overrides Agent per field; unset
	// fields resolve to empty. Both vars are always emitted, even when
	// empty: container env takes precedence over envFrom, so an explicit
	// (empty) value here overrides any KONVEYOR_GIT_AUTHOR_* smuggled in
	// through run.Spec.EnvFrom, and an empty pair makes the harness fall
	// back to its default identity rather than a forged or half-set one.
	gitName, gitEmail := resolveGitIdentity(agent, run)
	env = append(env,
		corev1.EnvVar{Name: gitAuthorNameEnv, Value: gitName},
		corev1.EnvVar{Name: gitAuthorEmailEnv, Value: gitEmail},
	)

	// Gateway credential mounting. One run = one gateway = one model.
	if run.Spec.Gateway != "" {
		var gateway konveyoriov1alpha1.Gateway
		gwKey := types.NamespacedName{Namespace: run.Namespace, Name: run.Spec.Gateway}
		if err := r.Get(ctx, gwKey, &gateway); err != nil {
			if errors.IsNotFound(err) {
				return nil, nil, fmt.Errorf("%w: %q", errGatewayNotFound, run.Spec.Gateway)
			}
			return nil, nil, fmt.Errorf("looking up Gateway %q: %w", run.Spec.Gateway, err)
		}
		// Verify the Gateway is currently Ready. Agent readiness can
		// be stale if the Gateway becomes unready after the Agent was
		// last reconciled.
		gwReady := meta.FindStatusCondition(gateway.Status.Conditions, ConditionTypeReady)
		if gwReady == nil || gwReady.Status != metav1.ConditionTrue {
			return nil, nil, fmt.Errorf("gateway %q is not Ready", run.Spec.Gateway)
		}
		env = append(env,
			corev1.EnvVar{Name: "KONVEYOR_LLM_PROVIDER", Value: gateway.Spec.Provider},
			corev1.EnvVar{Name: "KONVEYOR_LLM_ENDPOINT", Value: gateway.Spec.Endpoint},
			corev1.EnvVar{Name: "KONVEYOR_LLM_MODEL", Value: gateway.Spec.Model.Name},
		)

		// Mount the gateway's credential Secret. A single-key
		// credentialRef is a bearer-token-style credential injected as
		// KONVEYOR_LLM_API_KEY; a keyless one spans multiple env vars
		// (e.g. AWS SigV4) and is exposed whole via envFrom, with the
		// Secret's keys as the variable names.
		credSecretName := gateway.Spec.CredentialRef.SecretName
		if gateway.Spec.CredentialRef.Key != "" {
			env = append(env, corev1.EnvVar{
				Name: "KONVEYOR_LLM_API_KEY",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: credSecretName,
						},
						Key: gateway.Spec.CredentialRef.Key,
					},
				},
			})
		} else {
			envFrom = append(envFrom, corev1.EnvFromSource{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: credSecretName,
					},
				},
			})
		}
	}

	// Pass through user-specified env vars, but never let them override the
	// controller-managed git commit identity. A raw KONVEYOR_GIT_AUTHOR_*
	// in run.Spec.Env would otherwise win by last-key-wins and bypass
	// GitConfig's validation, forging authorship or leaving a half-set
	// identity (one field user-supplied, the other the harness default).
	for _, e := range run.Spec.Env {
		if e.Name == gitAuthorNameEnv || e.Name == gitAuthorEmailEnv {
			continue
		}
		env = append(env, e)
	}

	return env, envFrom, nil
}

// resolveGitIdentity computes the git commit identity (name, email) for a
// run. Name and email are one indivisible identity: an AgentRun's
// GitConfig replaces the Agent's wholesale rather than merging per field
// (a both-or-neither CRD constraint guarantees each GitConfig carries
// both). Empty return values mean "not configured" — the harness
// supplies its own default in that case.
func resolveGitIdentity(
	agent *konveyoriov1alpha1.Agent,
	run *konveyoriov1alpha1.AgentRun,
) (name, email string) {
	if gc := run.Spec.GitConfig; gc != nil {
		return gc.UserName, gc.UserEmail
	}
	if gc := agent.Spec.GitConfig; gc != nil {
		return gc.UserName, gc.UserEmail
	}
	return "", ""
}

// skillSources is the staged result of resolving an Agent's skill refs.
//
// Nothing is built here. An image was built ahead of time by whoever authored
// it, inline content is already bytes in the CR, and a git source is cloned at
// pod start — so the controller stays a stateless reconciler with no builder,
// no registry credentials and no network egress of its own.
type skillSources struct {
	// volumes and mounts stage each source read-only under /opt/skills-src.
	volumes []corev1.Volume
	mounts  []corev1.VolumeMount
	// inline maps a staged source name to its markdown, materialized as a
	// ConfigMap owned by the AgentRun.
	inline map[string]string
	// declared is the source list handed to the loader. Every source appears,
	// mounted or cloned, so the loader never has to infer what should be
	// there from what happens to be on disk.
	declared []skillSource
}

// skillSource is the loader's own Source type, not a copy of it. api/skill is
// the one package the controller and the harness both import, so the wire
// format of KONVEYOR_SKILL_SOURCES has a single definition and adding a field
// to it is a compile-time fact on both sides.
type skillSource = skill.Source

type skillGitSource = skill.GitSource

// resolveSkillVolumes resolves SkillCard and SkillCollection refs into staged
// pod sources. Each source is mounted read-only at /opt/skills-src/{name}; the
// loader assembles /opt/skills/{name}/SKILL.md from them.
//
// Sources are staged rather than mounted at their final path because one image
// may carry several skills, and because a skill's directory is decided by its
// own frontmatter name rather than by the SkillCard it arrived on.
func (r *AgentRunReconciler) resolveSkillVolumes(
	ctx context.Context,
	run *konveyoriov1alpha1.AgentRun,
	agent *konveyoriov1alpha1.Agent,
) (*skillSources, error) {
	namespace := run.Namespace
	out := &skillSources{inline: map[string]string{}}
	var errs []string
	seen := make(map[string]string)     // source name -> what it staged
	volNames := make(map[string]string) // volume name -> the source that took it

	// claim takes a source name and declares it to the loader. The name is a
	// staging label only: a genuine collision between the skills themselves is
	// caught by the loader, the only place that can see the frontmatter names
	// two sources actually declare.
	//
	// The name also becomes a path segment, in the staging mount here and in
	// the join the loader does on the other side, so it has to be one segment
	// and not a traversal.
	//
	// delivery is what the name resolves to. Reaching one skill twice, directly
	// and through a collection that carries it, is an ordinary way to pin a
	// card, so an identical second claim is a no-op; two different deliveries
	// under one name is a real conflict, because only one can occupy the
	// staging directory.
	claim := func(name, delivery, subPath, skillType string, git *skillGitSource) bool {
		if name == "" || name != path.Base(name) || name == "." || name == ".." {
			errs = append(errs, fmt.Sprintf(
				"skill source %q is not a usable directory name; it must be a single path segment", name))
			return false
		}
		// A SkillCard name is already a Kubernetes object name; a collection
		// entry's is only checked for MinLength. The name becomes a mount path
		// as well as a directory, and the API server rejects a mountPath
		// containing ':' with an error that names neither the skill nor the
		// collection it came from, so say it here instead.
		if problems := validation.IsDNS1123Subdomain(name); len(problems) > 0 {
			errs = append(errs, fmt.Sprintf(
				"skill source %q is not a usable directory name: %s", name, strings.Join(problems, "; ")))
			return false
		}
		if prev, dup := seen[name]; dup {
			if prev != delivery {
				errs = append(errs, fmt.Sprintf(
					"skill source %q is claimed by two different sources", name))
			}
			return false
		}
		seen[name] = delivery
		out.declared = append(out.declared, skillSource{
			Name:    name,
			SubPath: subPath,
			Type:    skillType,
			Git:     git,
		})
		return true
	}

	// stage adds the read-only mount a delivered source needs. Git sources
	// skip it: the loader clones into its own scratch space.
	stage := func(name string) string {
		// Two source names can sanitize to one volume name, and a pod with two
		// volumes of the same name is rejected by the API server with an error
		// that names neither skill.
		volName := sanitizeVolumeName("skill-" + name)
		if prev, taken := volNames[volName]; taken {
			errs = append(errs, fmt.Sprintf(
				"skill sources %q and %q both need the pod volume %q; rename one", prev, name, volName))
			return volName
		}
		volNames[volName] = name
		out.mounts = append(out.mounts, corev1.VolumeMount{
			Name:      volName,
			MountPath: skillsSrcDir + "/" + name,
			ReadOnly:  true,
		})
		return volName
	}

	addImage := func(name, subPath, skillType, image string) {
		if image == "" {
			errs = append(errs, fmt.Sprintf("skill %q has no resolved image", name))
			return
		}
		if !claim(name, fmt.Sprintf("image:%s:%s:%s", image, subPath, defaultedSkillType(skillType)),
			subPath, skillType, nil) {
			return
		}
		out.volumes = append(out.volumes, corev1.Volume{
			Name:         stage(name),
			VolumeSource: corev1.VolumeSource{Image: &corev1.ImageVolumeSource{Reference: image}},
		})
	}

	addInline := func(name, skillType, content string) {
		// Inline content is one SKILL.md, so there is nothing to select into.
		if !claim(name, fmt.Sprintf("inline:%s:%s", defaultedSkillType(skillType), content),
			"", skillType, nil) {
			return
		}
		out.inline[name] = content
		out.volumes = append(out.volumes, corev1.Volume{
			Name: stage(name),
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: inlineSkillConfigMapName(run.Name, name),
					},
				},
			},
		})
	}

	addGit := func(name, subPath, skillType string, git skillGitSource) {
		claim(name, fmt.Sprintf("git:%s:%s:%s:%s", git.URL, git.Ref, subPath, defaultedSkillType(skillType)),
			subPath, skillType, &git)
	}

	// addCard dispatches on which of the three sources a SkillCard declares.
	// The CRD guarantees exactly one is set. A card is one skill, so its
	// subPath and type travel with it whichever source it came from.
	addCard := func(name string, sc *konveyoriov1alpha1.SkillCard) {
		skillType := string(sc.Spec.Type)
		switch {
		case sc.Spec.Inline != "":
			addInline(name, skillType, sc.Spec.Inline)
		case sc.Spec.Source != "":
			addGit(name, sc.Spec.SubPath, skillType, skillGitSource{
				URL: sc.Spec.Source,
				Ref: sc.Spec.Ref,
			})
		default:
			addImage(name, sc.Spec.SubPath, skillType, sc.Status.ResolvedImage)
		}
	}

	// Resolve direct SkillCard refs.
	for _, ref := range agent.Spec.SkillCards {
		var sc konveyoriov1alpha1.SkillCard
		if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Ref}, &sc); err != nil {
			errs = append(errs, fmt.Sprintf("SkillCard %q: %v", ref.Ref, err))
			continue
		}
		addCard(sc.Name, &sc)
	}

	// Resolve SkillCollection refs.
	for _, ref := range agent.Spec.SkillCollections {
		var scol konveyoriov1alpha1.SkillCollection
		if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Ref}, &scol); err != nil {
			errs = append(errs, fmt.Sprintf("SkillCollection %q: %v", ref.Ref, err))
			continue
		}

		// An image collection has no spec.skills: the enumeration Job wrote a
		// SkillCard per skill in the image and status.resolvedSkills names
		// them. Referencing those cards is what makes pointing a collection at
		// an image reach an agent at all.
		if scol.Spec.Image != "" {
			if len(scol.Status.ResolvedSkills) == 0 {
				errs = append(errs, fmt.Sprintf(
					"SkillCollection %q has not finished enumerating %s", ref.Ref, scol.Spec.Image))
			}
			for _, cardName := range scol.Status.ResolvedSkills {
				var sc konveyoriov1alpha1.SkillCard
				if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: cardName}, &sc); err != nil {
					errs = append(errs, fmt.Sprintf("SkillCard %q (enumerated from collection %q): %v",
						cardName, ref.Ref, err))
					continue
				}
				addCard(sc.Name, &sc)
			}
			continue
		}

		for _, skillRef := range scol.Spec.Skills {
			switch {
			case skillRef.SkillCardRef != "":
				var sc konveyoriov1alpha1.SkillCard
				if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: skillRef.SkillCardRef}, &sc); err != nil {
					errs = append(errs, fmt.Sprintf("SkillCard %q (from collection %q): %v",
						skillRef.SkillCardRef, ref.Ref, err))
					continue
				}
				// Staged under the card's own name, the same name the direct
				// ref path uses, so a card reached both ways is one source
				// rather than two staging directories holding one skill --
				// which the loader would reject as a duplicate skill name.
				// A referenced card carries its own type; the entry's is
				// ignored, as the CRD says.
				addCard(sc.Name, &sc)
			case skillRef.Image != "":
				addImage(skillRef.Name, skillRef.SubPath, string(skillRef.Type), skillRef.Image)
			case skillRef.Source != "":
				addGit(skillRef.Name, skillRef.SubPath, string(skillRef.Type), skillGitSource{
					URL: skillRef.Source,
					Ref: skillRef.Ref,
				})
			default:
				// The CRD requires exactly one of the three to be present, and
				// a present-but-empty string satisfies it. Falling through
				// silently would hand the agent a pod missing a skill it was
				// told it has, with nothing anywhere saying so.
				errs = append(errs, fmt.Sprintf(
					"skill %q in collection %q has no source configured", skillRef.Name, ref.Ref))
			}
		}
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("skill resolution failed: %s", strings.Join(errs, "; "))
	}
	return out, nil
}

// defaultedSkillType fills in the load policy the SkillCard CRD defaults and a
// SkillCollection entry does not.
//
// Only for comparing two deliveries, never for what is declared to the loader:
// an unset type is left unset there so the loader applies the default in one
// place. But one skill reached both as a card and as a collection entry naming
// the same image arrives once as "skill" and once as "", and claim would
// otherwise read that as two sources fighting over one staging directory and
// fail the run.
func defaultedSkillType(skillType string) string {
	if skillType == "" {
		return string(konveyoriov1alpha1.SkillCardTypeSkill)
	}
	return skillType
}

// inlineSkillConfigMapName names the ConfigMap holding an inline skill's
// markdown.
//
// Scoped by AgentRun as well as SkillCard: the ConfigMap is owned by the run
// that mounts it, so two runs sharing one inline SkillCard must not share one
// ConfigMap. If they did, the second run would rewrite the owner reference and
// deleting it would garbage collect the ConfigMap out from under the first.
func inlineSkillConfigMapName(runName, skillCardName string) string {
	return sanitizeVolumeName(runName + "-skill-" + skillCardName)
}

// createInlineSkillConfigMaps materializes inline SkillCards. Inline content is
// already bytes in etcd, so there is nothing to build — it just needs a shape
// the kubelet can mount.
func (r *AgentRunReconciler) createInlineSkillConfigMaps(
	ctx context.Context,
	run *konveyoriov1alpha1.AgentRun,
	inline map[string]string,
) error {
	for name, content := range inline {
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Name:      inlineSkillConfigMapName(run.Name, name),
			Namespace: run.Namespace,
		}}
		if _, err := ctrl.CreateOrUpdate(ctx, r.Client, cm, func() error {
			cm.Labels = map[string]string{
				labelManagedBy: managedByLabel,
				labelAgentRun:  run.Name,
			}
			// A ConfigMap key cannot contain a path separator, so an inline
			// skill is a single SKILL.md and cannot ship supporting files.
			// Anything needing references/ has to be an image or a git source.
			cm.Data = map[string]string{skillFileKey: content}
			// Owned by the run, so it is collected with it.
			return ctrl.SetControllerReference(run, cm, r.Scheme)
		}); err != nil {
			return fmt.Errorf("writing ConfigMap for inline skill %q: %w", name, err)
		}
	}
	return nil
}

// skillLoaderContainer builds the init container that assembles and validates
// the skills root. It always runs, even with no skills, so there is one pod
// shape and one place that fails a run whose skills are unusable.
//
// It runs the controller's own image rather than the agent's, so an agent
// image is not required to carry our binary and the source list the controller
// writes is always read by a loader of the same version. Command names the
// binary rather than trusting an ENTRYPOINT, and both directories are explicit
// because this container does not inherit the env their defaults come from.
func skillLoaderContainer(image string, sources *skillSources, mounts []corev1.VolumeMount) corev1.Container {
	c := corev1.Container{
		Name:    skillLoaderContainerName,
		Image:   image,
		Command: []string{loaderBinary},
		// The loader writes the one line that matters to stderr. This lifts it
		// into the pod's status, so `kubectl describe pod` says which skill was
		// wrong without needing log access to a container that already exited.
		TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
		Args: []string{
			"load",
			"--src-dir", skillsSrcDir,
			"--dest-dir", skillsDir,
		},
		VolumeMounts: mounts,
	}
	if len(sources.declared) > 0 {
		// Marshaling a slice of fixed structs cannot fail.
		encoded, _ := json.Marshal(sources.declared)
		c.Env = append(c.Env, corev1.EnvVar{Name: skillSourcesEnv, Value: string(encoded)})
	}
	return c
}

// updatePhaseFromSandbox updates the AgentRun phase and ACPReady condition
// from the Sandbox, its pod, and the Sandbox's Ready condition.
//
// Phase follows the agent process: Pending until the sandbox pod is Running,
// then Running (one-way), then Succeeded/Failed when the Sandbox reports
// Finished. Whether the agent's ACP endpoint accepts connections is a
// separate fact — the sandbox pod's tcpSocket:4000 readiness probe feeding
// the Sandbox Ready condition — and is reported as the ACPReady condition,
// so a client dials on ACPReady=True, never on Phase.
//
// It returns the duration after which the run should be re-reconciled, or
// 0 for none. A non-zero requeue enforces the startup deadline for a pod
// that is slow to start without depending on a pod event.
func (r *AgentRunReconciler) updatePhaseFromSandbox(
	ctx context.Context,
	run *konveyoriov1alpha1.AgentRun,
	sandbox *sandboxv1beta1.Sandbox,
	pod *corev1.Pod,
) time.Duration {
	// Check Sandbox conditions for Finished state.
	for _, cond := range sandbox.Status.Conditions {
		if cond.Type == sandboxConditionFinished && cond.Status == metav1.ConditionTrue {
			stampCompletion(run, pod, sandbox)
			meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
				Type:               konveyoriov1alpha1.AgentRunConditionACPReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: run.Generation,
				Reason:             "Finished",
				Message:            "The run has finished; its ACP endpoint is gone",
			})

			// Capture the harness's opaque termination data (usage/cost
			// report). Stored verbatim, never interpreted (ADR 0018).
			if td := terminationDataFromPod(pod); td != nil {
				run.Status.TerminationData = td
			}

			// The harness may also write a human-readable failure message to
			// the termination log (e.g. a non-git source, #143); surface it
			// on the failure outcome, preferring it over the generic reason.
			r.setTerminalOutcome(run, pod, cond.Reason, r.lookupTerminationMessage(ctx, run))
			return 0
		}
	}

	// ACP readiness: the Sandbox is Ready once its pod passes readiness (the
	// agent container's ACP tcpSocket probe) and its headless Service exists.
	acpReady := metav1.Condition{
		Type:               konveyoriov1alpha1.AgentRunConditionACPReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: run.Generation,
		Reason:             "NotListening",
		Message:            fmt.Sprintf("Waiting for Sandbox %q to become Ready", sandbox.Name),
	}
	if ready := meta.FindStatusCondition(sandbox.Status.Conditions,
		string(sandboxv1beta1.SandboxConditionReady)); ready != nil && ready.Status == metav1.ConditionTrue {
		acpReady.Status = metav1.ConditionTrue
		acpReady.Reason = "Listening"
		acpReady.Message = fmt.Sprintf("ACP endpoint %s.%s.svc:%d accepts connections",
			sandbox.Name, sandbox.Namespace, acpPort)
	} else if ready != nil && ready.Message != "" {
		acpReady.Message = fmt.Sprintf("%s: %s", acpReady.Message, ready.Message)
	}
	meta.SetStatusCondition(&run.Status.Conditions, acpReady)

	// Phase: Running once the agent process is executing, i.e. the sandbox
	// pod is Running. One-way — a later pod state change does not regress
	// a Running run.
	if run.Status.Phase == konveyoriov1alpha1.AgentRunPhaseRunning {
		return 0
	}

	// Fail-fast: a pod that cannot start (image pull, container config, or
	// crash loop) will never reach Running, so fail the run now instead of
	// waiting on it. Checked before the pod-phase gate because a container
	// stuck in CrashLoopBackOff can still put the pod in the Running phase.
	if reason, message, fatal := podStartupProblem(pod); fatal {
		run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
		stampCompletion(run, pod, sandbox)
		setRunSucceeded(run, metav1.ConditionFalse, reason, message)
		return 0
	}

	if pod == nil || pod.Status.Phase != corev1.PodRunning {
		// The pod is not running yet. If a startup deadline is configured
		// and has elapsed, fail the run; otherwise stay Pending and requeue
		// so the deadline is enforced even absent a further pod event.
		if deadline, ok := r.effectiveStartupDeadline(run); ok {
			remaining := deadline - time.Since(sandbox.CreationTimestamp.Time)
			if remaining <= 0 {
				run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
				stampCompletion(run, pod, sandbox)
				detail := podStartupDetail(pod)
				setRunSucceeded(run, metav1.ConditionFalse,
					konveyoriov1alpha1.AgentRunReasonStartupDeadlineExceeded,
					fmt.Sprintf("Pod %q did not reach a running state within %s: %s",
						sandbox.Name, deadline, detail))
				return 0
			}
			run.Status.Phase = konveyoriov1alpha1.AgentRunPhasePending
			setRunSucceeded(run, metav1.ConditionUnknown, "PodNotRunning",
				fmt.Sprintf("Waiting for sandbox pod %q to run (%s)",
					sandbox.Name, podStartupDetail(pod)))
			return remaining
		}
		run.Status.Phase = konveyoriov1alpha1.AgentRunPhasePending
		setRunSucceeded(run, metav1.ConditionUnknown, "PodNotRunning",
			fmt.Sprintf("Waiting for sandbox pod %q to run (%s)",
				sandbox.Name, podStartupDetail(pod)))
		return 0
	}
	run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseRunning
	start := podStartTime(pod, sandbox.CreationTimestamp)
	run.Status.StartTime = &start
	setRunSucceeded(run, metav1.ConditionUnknown, konveyoriov1alpha1.AgentRunReasonRunning,
		"Agent is running")
	return 0
}

// stampCompletion records the terminal timing on a finished run: the
// completion time now, a start-time fallback for runs that finished before
// the pod was ever seen running, and the resulting duration.
func stampCompletion(run *konveyoriov1alpha1.AgentRun, pod *corev1.Pod, sandbox *sandboxv1beta1.Sandbox) {
	now := metav1.Now()
	run.Status.CompletionTime = &now
	if run.Status.StartTime == nil {
		// Finished before we ever saw the pod running: the pod's own start,
		// else the Sandbox creation, so Duration still reflects wall time.
		start := podStartTime(pod, sandbox.CreationTimestamp)
		run.Status.StartTime = &start
	}
	duration := int64(now.Sub(run.Status.StartTime.Time).Seconds())
	run.Status.Duration = &duration
}

// fatalWaitingReasons are container Waiting.Reason values that mean the pod
// will not start without intervention: the image cannot be pulled or named,
// the container cannot be configured, or it crashes on every start. They are
// settled states — kubelet has already retried into a back-off, or the
// config is permanently wrong — so the run is failed rather than waited on.
//
// CrashLoopBackOff is effectively unreachable today: sandbox pods run with
// RestartPolicyNever, so a crashing container terminates (surfaced through the
// Sandbox Finished / setTerminalOutcome path) rather than entering a waiting
// back-off. It is kept as a guard against a future restart-policy change.
var fatalWaitingReasons = map[string]bool{
	"ImagePullBackOff":           true,
	"InvalidImageName":           true,
	"CreateContainerConfigError": true,
	"CrashLoopBackOff":           true,
}

// podStartupProblem inspects a not-yet-running pod for a fatal startup
// failure. It returns a machine reason (the kubelet's own waiting reason),
// a human message naming the container, and fatal=true when the pod will
// not start on its own. Init containers are checked first: the skill loader
// runs there, and a bad loader image never lets the agent container start.
func podStartupProblem(pod *corev1.Pod) (reason, message string, fatal bool) {
	if pod == nil {
		return "", "", false
	}
	statuses := make([]corev1.ContainerStatus, 0,
		len(pod.Status.InitContainerStatuses)+len(pod.Status.ContainerStatuses))
	statuses = append(statuses, pod.Status.InitContainerStatuses...)
	statuses = append(statuses, pod.Status.ContainerStatuses...)
	for _, cs := range statuses {
		w := cs.State.Waiting
		if w == nil || !fatalWaitingReasons[w.Reason] {
			continue
		}
		msg := fmt.Sprintf("container %q is stuck on %s", cs.Name, w.Reason)
		if w.Message != "" {
			msg = fmt.Sprintf("%s: %s", msg, w.Message)
		}
		return w.Reason, msg, true
	}
	return "", "", false
}

// podStartupDetail returns a short human description of why a pod is not
// running yet, for the deadline-exceeded and Pending messages. It surfaces
// an unschedulable condition or a container's current waiting reason, and
// otherwise falls back to the pod phase.
func podStartupDetail(pod *corev1.Pod) string {
	if pod == nil {
		return "pod not created yet"
	}
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse &&
			c.Reason == corev1.PodReasonUnschedulable {
			if c.Message != "" {
				return fmt.Sprintf("unschedulable: %s", c.Message)
			}
			return "unschedulable"
		}
	}
	statuses := make([]corev1.ContainerStatus, 0,
		len(pod.Status.InitContainerStatuses)+len(pod.Status.ContainerStatuses))
	statuses = append(statuses, pod.Status.InitContainerStatuses...)
	statuses = append(statuses, pod.Status.ContainerStatuses...)
	for _, cs := range statuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			return fmt.Sprintf("container %q: %s", cs.Name, cs.State.Waiting.Reason)
		}
	}
	return fmt.Sprintf("pod phase %s", pod.Status.Phase)
}

// effectiveStartupDeadline returns the run's startup deadline: its own
// spec.startupDeadlineSeconds when set to a positive value, else the
// controller default. ok is false when no deadline applies.
func (r *AgentRunReconciler) effectiveStartupDeadline(run *konveyoriov1alpha1.AgentRun) (time.Duration, bool) {
	if run.Spec.StartupDeadlineSeconds != nil && *run.Spec.StartupDeadlineSeconds > 0 {
		return time.Duration(*run.Spec.StartupDeadlineSeconds) * time.Second, true
	}
	if r.DefaultStartupDeadline != nil && *r.DefaultStartupDeadline > 0 {
		return *r.DefaultStartupDeadline, true
	}
	return 0, false
}

// Harness exit-code contract (ADR 0011/0018). Additional codes may be
// added later; any non-zero code stops a workflow.
const (
	harnessExitSucceeded    = 0
	harnessExitLimitReached = 2
)

// setTerminalOutcome sets the AgentRun's terminal phase and the
// Succeeded condition from the harness exit code (ADR 0018), falling
// back to the Sandbox's coarse Finished reason when the pod's container
// exit code is unavailable.
//
// Note the exit-2 remap: a limit-reached run exits non-zero, so the pod
// is Failed and the Sandbox reports a non-PodSucceeded reason — the
// controller must read the container exit code to tell "stopped on
// budget" (Succeeded=False, LimitReached) from a genuine error
// (Succeeded=False, Failed). Only exit 0 is a clean success.
//
// failureMessage, when non-empty, is the harness's human-readable
// termination message (e.g. a non-git source, #143); it is preferred over
// the generic reason on a failure outcome.
func (r *AgentRunReconciler) setTerminalOutcome(
	run *konveyoriov1alpha1.AgentRun,
	pod *corev1.Pod,
	sandboxReason string,
	failureMessage string,
) {
	exitCode, haveExit := agentExitCode(pod)

	switch {
	case haveExit && exitCode == harnessExitLimitReached:
		run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
		setRunSucceeded(run, metav1.ConditionFalse, konveyoriov1alpha1.AgentRunReasonLimitReached,
			"Execution limit reached; the agent committed a handoff")
	case (haveExit && exitCode == harnessExitSucceeded) ||
		(!haveExit && sandboxReason == sandboxFinishedReasonSucceeded):
		run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseSucceeded
		setRunSucceeded(run, metav1.ConditionTrue, konveyoriov1alpha1.AgentRunReasonSucceeded,
			"Agent run completed successfully")
	default:
		run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
		message := fmt.Sprintf("Sandbox finished with reason: %s", sandboxReason)
		if haveExit {
			message = fmt.Sprintf("Agent exited with code %d", exitCode)
		}
		// Prefer the harness's human-readable failure message when present.
		if failureMessage != "" {
			message = failureMessage
		}
		setRunSucceeded(run, metav1.ConditionFalse, konveyoriov1alpha1.AgentRunReasonFailed, message)
	}
}

// setRunSucceeded sets the AgentRun's Succeeded condition — the single
// terminal-outcome signal (ADR 0018). Unknown while the run is still in
// progress, True/False once it ends. AgentRun carries no Ready condition;
// every progress and outcome state rides Succeeded (serving is the
// separate ACPReady condition).
func setRunSucceeded(
	run *konveyoriov1alpha1.AgentRun,
	status metav1.ConditionStatus,
	reason, message string,
) {
	meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
		Type:               konveyoriov1alpha1.AgentRunConditionSucceeded,
		Status:             status,
		ObservedGeneration: run.Generation,
		Reason:             reason,
		Message:            message,
	})
}

// terminationDataFromPod returns the agent container's termination message
// as an opaque RawExtension for AgentRun.status.terminationData, but only
// when it is a JSON object. The CRD types terminationData as an object, so
// an array or scalar message would get the whole status patch (and
// setTerminalOutcome with it) rejected; unmarshalling into a map succeeds
// only for objects. Returns nil when there is no termination, the message
// is empty, or it is not a JSON object.
func terminationDataFromPod(pod *corev1.Pod) *runtime.RawExtension {
	term := agentContainerTermination(pod)
	if term == nil || term.Message == "" {
		return nil
	}
	var obj map[string]any
	if json.Unmarshal([]byte(term.Message), &obj) != nil {
		return nil
	}
	return &runtime.RawExtension{Raw: []byte(term.Message)}
}

// agentContainerTermination returns the terminated state of the agent
// container, or nil if the pod is absent or the container has not
// terminated.
func agentContainerTermination(pod *corev1.Pod) *corev1.ContainerStateTerminated {
	if pod == nil {
		return nil
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == agentContainerName && cs.State.Terminated != nil {
			return cs.State.Terminated
		}
	}
	return nil
}

// agentExitCode returns the agent container's exit code and whether it
// was available.
func agentExitCode(pod *corev1.Pod) (int32, bool) {
	if term := agentContainerTermination(pod); term != nil {
		return term.ExitCode, true
	}
	return 0, false
}

// podStartTime is when the agent container started running, else when the
// pod was accepted, else the given fallback.
func podStartTime(pod *corev1.Pod, fallback metav1.Time) metav1.Time {
	if pod != nil {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Running != nil && !cs.State.Running.StartedAt.IsZero() {
				return cs.State.Running.StartedAt
			}
		}
		if pod.Status.StartTime != nil {
			return *pod.Status.StartTime
		}
	}
	return fallback
}

// lookupTerminationMessage lists the run's pods and returns the agent
// container's terminated-state message, or "" if none is available. Errors
// listing pods are swallowed — the termination payload is best-effort detail
// and must never block the phase update.
func (r *AgentRunReconciler) lookupTerminationMessage(
	ctx context.Context,
	run *konveyoriov1alpha1.AgentRun,
) string {
	var pods corev1.PodList
	if err := r.apiReader.List(ctx, &pods,
		client.InNamespace(run.Namespace),
		client.MatchingLabels{labelAgentRun: run.Name},
	); err != nil {
		log.FromContext(ctx).V(1).Info("listing pods for termination message failed",
			"agentRun", run.Name, "error", err)
		return ""
	}
	return podTerminationMessage(pods.Items)
}

// podTerminationMessage returns the "agent" container's terminated-state
// message across the given pods, or "" if no such terminated container is
// found. It is a pure function to keep the extraction logic unit-testable.
func podTerminationMessage(pods []corev1.Pod) string {
	for i := range pods {
		for _, cs := range pods[i].Status.ContainerStatuses {
			if cs.Name != agentContainerName {
				continue
			}
			if cs.State.Terminated != nil && cs.State.Terminated.Message != "" {
				return cs.State.Terminated.Message
			}
		}
	}
	return ""
}

// patchRunStatus patches the AgentRun status.
func (r *AgentRunReconciler) patchRunStatus(
	ctx context.Context,
	run *konveyoriov1alpha1.AgentRun,
	original *konveyoriov1alpha1.AgentRun,
) (ctrl.Result, error) {
	if err := r.Status().Patch(ctx, run, client.MergeFrom(original)); err != nil {
		log.FromContext(ctx).Error(err, "Failed to patch AgentRun status",
			"agentRun", run.Name)
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// isTerminalPhase reports whether an AgentRun phase is terminal — the run has
// finished and no further execution work happens.
func isTerminalPhase(phase konveyoriov1alpha1.AgentRunPhase) bool {
	return phase == konveyoriov1alpha1.AgentRunPhaseSucceeded ||
		phase == konveyoriov1alpha1.AgentRunPhaseFailed
}

// effectiveTTL resolves a terminal AgentRun's lifetime: the run's own
// spec.ttlSecondsAfterFinished wins, then the controller's
// DefaultTTLAfterFinished. The bool is false when neither is set, meaning GC
// is disabled and the run is kept until deleted manually.
func (r *AgentRunReconciler) effectiveTTL(run *konveyoriov1alpha1.AgentRun) (time.Duration, bool) {
	if run.Spec.TTLSecondsAfterFinished != nil {
		return time.Duration(*run.Spec.TTLSecondsAfterFinished) * time.Second, true
	}
	if r.DefaultTTLAfterFinished != nil {
		return *r.DefaultTTLAfterFinished, true
	}
	return 0, false
}

// reconcileTTL garbage-collects a terminal AgentRun once its TTL elapses.
// With no effective TTL the run is kept. The finish anchor is CompletionTime;
// a terminal run that never recorded one (e.g. a validation failure before a
// Sandbox existed) gets it stamped now so expiry is deterministic across
// controller restarts. Before the TTL elapses the reconcile is requeued for
// the remaining time; once it has, the run is deleted — cascading to
// everything it owns (Sandbox, pod, per-run ConfigMaps/Secrets) via owner
// references.
func (r *AgentRunReconciler) reconcileTTL(
	ctx context.Context,
	run *konveyoriov1alpha1.AgentRun,
	original *konveyoriov1alpha1.AgentRun,
) (ctrl.Result, error) {
	ttl, ok := r.effectiveTTL(run)
	if !ok {
		return ctrl.Result{}, nil
	}

	// Anchor the clock: a terminal run with no CompletionTime records one now.
	if run.Status.CompletionTime == nil {
		now := metav1.Now()
		run.Status.CompletionTime = &now
		return r.patchRunStatus(ctx, run, original)
	}

	if remaining := time.Until(run.Status.CompletionTime.Add(ttl)); remaining > 0 {
		return ctrl.Result{RequeueAfter: remaining}, nil
	}

	log.FromContext(ctx).Info("Deleting finished AgentRun (TTL elapsed)",
		"agentRun", run.Name, "ttl", ttl.String(), "completionTime", run.Status.CompletionTime)
	if err := r.Delete(ctx, run); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}

// generateSecretKey generates a random hex-encoded secret key.
func generateSecretKey() (string, error) {
	b := make([]byte, secretKeyLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AgentRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Direct API reader for the best-effort pod termination-message lookup,
	// so we don't start a cluster-wide Pod informer via the manager cache.
	r.apiReader = mgr.GetAPIReader()

	// Index AgentRuns by agentRef for efficient reverse lookup when
	// an Agent changes.
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&konveyoriov1alpha1.AgentRun{},
		agentRunRefIndexField,
		func(obj client.Object) []string {
			run := obj.(*konveyoriov1alpha1.AgentRun)
			return []string{run.Spec.AgentRef}
		},
	); err != nil {
		return fmt.Errorf("indexing %s: %w", agentRunRefIndexField, err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&konveyoriov1alpha1.AgentRun{}).
		Owns(&sandboxv1beta1.Sandbox{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.ConfigMap{}).
		// Owner references remain authoritative for ordinary reconciliation.
		// These additional label maps surface the exceptional ownerless child
		// after a manager restart; workqueue de-duplication handles the small
		// event overlap if ownership is repaired concurrently.
		Watches(
			&sandboxv1beta1.Sandbox{},
			handler.EnqueueRequestsFromMapFunc(runForLabeledResource),
			builder.WithPredicates(predicate.NewPredicateFuncs(isOwnerlessManagedRunResource)),
		).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(runForLabeledResource),
			builder.WithPredicates(predicate.NewPredicateFuncs(isOwnerlessManagedRunResource)),
		).
		Watches(
			&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(runForLabeledResource),
			builder.WithPredicates(predicate.NewPredicateFuncs(isOwnerlessManagedRunResource)),
		).
		Watches(
			&konveyoriov1alpha1.Agent{},
			handler.EnqueueRequestsFromMapFunc(r.findRunsForAgent),
		).
		// Sandbox pods carry the run's name as a label (set on the Sandbox
		// PodTemplate); their phase drives Running, and the manager's cache
		// is restricted to labeled pods (see SandboxPodCacheOptions).
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(runForLabeledResource),
			builder.WithPredicates(predicate.NewPredicateFuncs(isSandboxPod)),
		).
		Named("agentrun").
		Complete(r)
}

// isSandboxPod reports whether obj is a sandbox pod created for an AgentRun.
func isSandboxPod(obj client.Object) bool {
	_, ok := obj.GetLabels()[labelAgentRun]
	return ok
}

// runForLabeledResource maps a controller-managed child or sandbox pod to the
// AgentRun named by its identifying label. Unlike an owner-only map, this also
// works when an owner reference is missing and the child needs to be swept.
func runForLabeledResource(_ context.Context, obj client.Object) []reconcile.Request {
	name, ok := obj.GetLabels()[labelAgentRun]
	if !ok {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{
		Namespace: obj.GetNamespace(),
		Name:      name,
	}}}
}

// SandboxPodCacheOptions restricts the manager's Pod cache to sandbox pods,
// so watching Pods for AgentRun readiness does not mean caching every pod
// in the cluster.
func SandboxPodCacheOptions() map[client.Object]cache.ByObject {
	req, err := labels.NewRequirement(labelAgentRun, selection.Exists, nil)
	if err != nil {
		panic(err) // static input; cannot fail
	}
	return map[client.Object]cache.ByObject{
		&corev1.Pod{}: {Label: labels.NewSelector().Add(*req)},
	}
}

// findRunsForAgent returns reconcile requests for all non-terminal AgentRuns
// that reference the given Agent.
func (r *AgentRunReconciler) findRunsForAgent(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	agent, ok := obj.(*konveyoriov1alpha1.Agent)
	if !ok {
		return nil
	}

	var runList konveyoriov1alpha1.AgentRunList
	if err := r.List(ctx, &runList,
		client.InNamespace(agent.Namespace),
		client.MatchingFields{agentRunRefIndexField: agent.Name},
	); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list AgentRuns for Agent", "agent", agent.Name)
		return nil
	}

	var requests []reconcile.Request
	for _, run := range runList.Items {
		// Only re-reconcile non-terminal runs.
		if isTerminalPhase(run.Status.Phase) {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: run.Namespace,
				Name:      run.Name,
			},
		})
	}

	return requests
}

// volumeNameRegex matches characters invalid in RFC 1123 volume names.
var volumeNameRegex = regexp.MustCompile(`[^a-z0-9-]`)

// sanitizeVolumeName converts a name to a valid Kubernetes volume name
// (RFC 1123: lowercase alphanumeric + hyphens, max 63 chars).
//
// The result names pod volumes, ConfigMaps and enumeration Jobs, so it has to
// be injective: rewriting is what makes two inputs one name, and a collision
// there silently makes two things the same object rather than failing. A name
// that already satisfies the rules is returned untouched; anything the rewrite
// touched carries a hash of what it started as. Dots are the case that bites,
// since they are legal in an object name and the regex collapses them to
// hyphens, so "my.collection" and "my-collection" would otherwise meet.
func sanitizeVolumeName(name string) string {
	sanitized := strings.Trim(volumeNameRegex.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if sanitized == name && len(name) <= 63 {
		return name
	}
	suffix := "-" + hex.EncodeToString(sha256Prefix(name))
	if sanitized == "" {
		sanitized = "x"
	}
	if len(sanitized)+len(suffix) > 63 {
		sanitized = strings.TrimRight(sanitized[:63-len(suffix)], "-")
	}
	return sanitized + suffix
}

func sha256Prefix(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:4]
}
