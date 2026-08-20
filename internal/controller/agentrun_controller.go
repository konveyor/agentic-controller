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
	"fmt"
	"regexp"
	"strings"

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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"

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

	// paramsVolumeName is the name of the ConfigMap volume carrying
	// /run/konveyor/params.json (ADR 0009).
	paramsVolumeName = "konveyor-params"

	// agentContainerName is the name of the agent container in the Sandbox pod.
	agentContainerName = "agent"

	// sandboxFinishedReasonSucceeded is the Sandbox condition reason for
	// success. Must match Agent Sandbox's SandboxReasonPodSucceeded constant.
	sandboxFinishedReasonSucceeded = "PodSucceeded"

	// agentRunRefIndexField is the field index for looking up AgentRuns by agentRef.
	agentRunRefIndexField = ".spec.agentRef"
)

// AgentRunReconciler reconciles an AgentRun object.
type AgentRunReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=konveyor.io,resources=agentruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=konveyor.io,resources=agentruns/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=konveyor.io,resources=agentruns/finalizers,verbs=update
// +kubebuilder:rbac:groups=agents.x-k8s.io,resources=sandboxes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

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
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.V(1).Info("Reconciling AgentRun", "name", run.Name)

	original := run.DeepCopy()
	run.Status.ObservedGeneration = run.Generation

	// If the run is already terminal, nothing to do.
	if run.Status.Phase == konveyoriov1alpha1.AgentRunPhaseSucceeded ||
		run.Status.Phase == konveyoriov1alpha1.AgentRunPhaseFailed {
		return ctrl.Result{}, nil
	}

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

	// Validate params against Agent declarations.
	if err := r.validateParams(&run, &agent); err != nil {
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

	// If no Sandbox exists yet, create one.
	if run.Status.SandboxName == "" {
		sandboxName, err := r.createSandbox(ctx, &run, &agent)
		if err != nil {
			logger.Error(err, "Failed to create Sandbox", "agentRun", run.Name, "agent", agent.Name)
			// Transient — the reconcile requeues with backoff — so the
			// run is not yet failed; Succeeded stays Unknown.
			setRunSucceeded(&run, metav1.ConditionUnknown, "SandboxCreationFailed",
				fmt.Sprintf("Failed to create Sandbox for Agent %q: %v", agent.Name, err))
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
	r.updatePhaseFromSandbox(&run, &sandbox, pod)

	return r.patchRunStatus(ctx, &run, original)
}

// validateParams checks that supplied params match Agent declarations.
func (r *AgentRunReconciler) validateParams(
	run *konveyoriov1alpha1.AgentRun,
	agent *konveyoriov1alpha1.Agent,
) error {
	// Build a map of declared params.
	declared := make(map[string]konveyoriov1alpha1.Param)
	for _, p := range agent.Spec.Params {
		declared[p.Name] = p
	}

	// Check that all supplied params are declared.
	for _, p := range run.Spec.Params {
		if _, ok := declared[p.Name]; !ok {
			return fmt.Errorf("param %q is not declared by Agent %q", p.Name, agent.Name)
		}
	}

	// Check that all required params (without defaults) are supplied.
	supplied := make(map[string]bool)
	for _, p := range run.Spec.Params {
		supplied[p.Name] = true
	}
	for _, p := range agent.Spec.Params {
		if p.Required && p.Default == "" && !supplied[p.Name] {
			return fmt.Errorf("required param %q not supplied", p.Name)
		}
	}

	// Validate type coercion and prompt/instruction substitution up
	// front. These are permanent config errors (a non-numeric value for
	// a number param, a reference to an undeclared param), so surfacing
	// them here fails the run terminally with InvalidParams rather than
	// requeuing forever as SandboxCreationFailed.
	_, scopes, err := buildParams(run, agent)
	if err != nil {
		return err
	}
	if _, _, err := renderPromptAndInstructions(agent, run, scopes); err != nil {
		return err
	}

	return nil
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

// validateGateway checks that the selected gateway is in the Agent's
// available gateway set. The Agent controller already watches Gateway
// CRs and won't report Ready if a referenced Gateway is missing, so
// the "Agent not Ready" check upstream catches dangling references.
// This function validates the AgentRun's selection against the Agent's
// declared set only — it does not re-verify the Gateway CR exists.
func (r *AgentRunReconciler) validateGateway(
	run *konveyoriov1alpha1.AgentRun,
	agent *konveyoriov1alpha1.Agent,
) error {
	if run.Spec.Gateway == "" {
		// Default to the Agent's only gateway when exactly one is
		// declared. When multiple are available, require explicit
		// selection so the run fails fast instead of dying at runtime
		// on missing KONVEYOR_LLM_MODEL.
		switch len(agent.Spec.Gateways) {
		case 1:
			run.Spec.Gateway = agent.Spec.Gateways[0].Ref
		default:
			return fmt.Errorf("agent %q declares %d gateways; select one via spec.gateway",
				agent.Name, len(agent.Spec.Gateways))
		}
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

	// Resolve parameters into params.json content and the substitution
	// scopes used for prompt/instructions rendering (ADR 0009/0018). A
	// coercion or unresolved-reference failure aborts before any Sandbox
	// is created.
	params, scopes, err := buildParams(run, agent)
	if err != nil {
		return "", fmt.Errorf("resolving params: %w", err)
	}

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

	// Resolve skill images for ImageVolumes.
	volumes, volumeMounts, err := r.resolveSkillVolumes(ctx, agent, run.Namespace)
	if err != nil {
		return "", fmt.Errorf("resolving skill volumes: %w", err)
	}
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
				labelManagedBy:                managedByLabel,
				"app.kubernetes.io/component": "agent-sandbox",
				labelAgentRun:                 run.Name,
				labelAgent:                    agent.Name,
			},
		},
		Spec: sandboxv1beta1.SandboxSpec{
			PodTemplate: sandboxv1beta1.PodTemplate{
				// Agent Sandbox v0.5.0 copies only PodTemplate metadata
				// onto the pod, so mirror the identifying labels here to
				// make the pod discoverable by AgentRun / Agent name.
				ObjectMeta: sandboxv1beta1.PodMetadata{
					Labels: map[string]string{
						labelAgentRun: run.Name,
						labelAgent:    agent.Name,
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
						},
					},
					Volumes: volumes,
				},
			},
			Service: &serviceEnabled,
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

	cmName := run.Name + "-params"
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: run.Namespace,
			Labels: map[string]string{
				labelManagedBy: managedByLabel,
				labelAgentRun:  run.Name,
			},
		},
		Data: map[string]string{paramsFileName: string(content)},
	}
	if err := ctrl.SetControllerReference(run, cm, r.Scheme); err != nil {
		return corev1.Volume{}, corev1.VolumeMount{}, fmt.Errorf("setting ConfigMap owner reference: %w", err)
	}
	if err := r.Create(ctx, cm); err != nil && !errors.IsAlreadyExists(err) {
		return corev1.Volume{}, corev1.VolumeMount{}, fmt.Errorf("creating params ConfigMap: %w", err)
	}

	volume := corev1.Volume{
		Name: paramsVolumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
				Items: []corev1.KeyToPath{
					{Key: paramsFileName, Path: paramsFileName},
				},
			},
		},
	}
	mount := corev1.VolumeMount{
		Name:      paramsVolumeName,
		MountPath: "/run/konveyor",
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

	// Gateway credential mounting. One run = one gateway = one model.
	if run.Spec.Gateway != "" {
		var gateway konveyoriov1alpha1.Gateway
		gwKey := types.NamespacedName{Namespace: run.Namespace, Name: run.Spec.Gateway}
		if err := r.Get(ctx, gwKey, &gateway); err != nil {
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

	// Pass through user-specified env vars.
	env = append(env, run.Spec.Env...)

	return env, envFrom, nil
}

// resolveSkillVolumes resolves SkillCard and SkillCollection refs to
// ImageVolume specs. Each resolved skill is mounted at
// /opt/skills/{name}/. Returns an error if any skill cannot be resolved.
func (r *AgentRunReconciler) resolveSkillVolumes(
	ctx context.Context,
	agent *konveyoriov1alpha1.Agent,
	namespace string,
) ([]corev1.Volume, []corev1.VolumeMount, error) {
	var volumes []corev1.Volume
	var mounts []corev1.VolumeMount
	var errs []string
	seen := make(map[string]bool) // deduplicate by skill name

	addSkill := func(name, image string) {
		if seen[name] {
			return
		}
		if image == "" {
			errs = append(errs, fmt.Sprintf("skill %q has no resolved image", name))
			return
		}
		seen[name] = true
		volName := sanitizeVolumeName("skill-" + name)
		volumes = append(volumes, corev1.Volume{
			Name: volName,
			VolumeSource: corev1.VolumeSource{
				Image: &corev1.ImageVolumeSource{
					Reference: image,
				},
			},
		})
		mounts = append(mounts, corev1.VolumeMount{
			Name:      volName,
			MountPath: "/opt/skills/" + name,
			ReadOnly:  true,
		})
	}

	// Resolve direct SkillCard refs.
	for _, ref := range agent.Spec.SkillCards {
		var sc konveyoriov1alpha1.SkillCard
		if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Ref}, &sc); err != nil {
			errs = append(errs, fmt.Sprintf("SkillCard %q: %v", ref.Ref, err))
			continue
		}
		addSkill(sc.Name, sc.Status.ResolvedImage)
	}

	// Resolve SkillCollection refs.
	for _, ref := range agent.Spec.SkillCollections {
		var scol konveyoriov1alpha1.SkillCollection
		if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Ref}, &scol); err != nil {
			errs = append(errs, fmt.Sprintf("SkillCollection %q: %v", ref.Ref, err))
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
				addSkill(skillRef.Name, sc.Status.ResolvedImage)
			case skillRef.Image != "":
				addSkill(skillRef.Name, skillRef.Image)
			}
		}
	}

	if len(errs) > 0 {
		return nil, nil, fmt.Errorf("skill resolution failed: %s", strings.Join(errs, "; "))
	}

	return volumes, mounts, nil
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
func (r *AgentRunReconciler) updatePhaseFromSandbox(
	run *konveyoriov1alpha1.AgentRun,
	sandbox *sandboxv1beta1.Sandbox,
	pod *corev1.Pod,
) {
	// Check Sandbox conditions for Finished state.
	for _, cond := range sandbox.Status.Conditions {
		if cond.Type == "Finished" && cond.Status == metav1.ConditionTrue {
			now := metav1.Now()
			run.Status.CompletionTime = &now
			if run.Status.StartTime == nil {
				// Finished before we ever saw the pod running: the pod's
				// own start, else the Sandbox creation, so Duration still
				// reflects the run's wall time.
				start := podStartTime(pod, sandbox.CreationTimestamp)
				run.Status.StartTime = &start
			}
			duration := int64(now.Sub(run.Status.StartTime.Time).Seconds())
			run.Status.Duration = &duration
			meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
				Type:               konveyoriov1alpha1.AgentRunConditionACPReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: run.Generation,
				Reason:             "Finished",
				Message:            "The run has finished; its ACP endpoint is gone",
			})

			// Capture the harness's opaque termination data (usage/cost
			// report). Stored verbatim, never interpreted (ADR 0018).
			if term := agentContainerTermination(pod); term != nil && json.Valid([]byte(term.Message)) {
				run.Status.TerminationData = &runtime.RawExtension{Raw: []byte(term.Message)}
			}

			r.setTerminalOutcome(run, pod, cond.Reason)
			return
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
		return
	}
	if pod == nil || pod.Status.Phase != corev1.PodRunning {
		run.Status.Phase = konveyoriov1alpha1.AgentRunPhasePending
		podPhase := "not created yet"
		if pod != nil {
			podPhase = string(pod.Status.Phase)
		}
		setRunSucceeded(run, metav1.ConditionUnknown, "PodNotRunning",
			fmt.Sprintf("Waiting for sandbox pod %q to run (%s)", sandbox.Name, podPhase))
		return
	}
	run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseRunning
	start := podStartTime(pod, sandbox.CreationTimestamp)
	run.Status.StartTime = &start
	setRunSucceeded(run, metav1.ConditionUnknown, konveyoriov1alpha1.AgentRunReasonRunning,
		"Agent is running")
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
// exit code is unavailable. It also mirrors the outcome onto the legacy
// Ready condition during the phase→Succeeded transition.
//
// Note the exit-2 remap: a limit-reached run exits non-zero, so the pod
// is Failed and the Sandbox reports a non-PodSucceeded reason — the
// controller must read the container exit code to tell "stopped on
// budget" (Succeeded=False, LimitReached) from a genuine error
// (Succeeded=False, Failed). Only exit 0 is a clean success.
func (r *AgentRunReconciler) setTerminalOutcome(
	run *konveyoriov1alpha1.AgentRun,
	pod *corev1.Pod,
	sandboxReason string,
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
		Watches(
			&konveyoriov1alpha1.Agent{},
			handler.EnqueueRequestsFromMapFunc(r.findRunsForAgent),
		).
		// Sandbox pods carry the run's name as a label (set on the Sandbox
		// PodTemplate); their phase drives Running, and the manager's cache
		// is restricted to labeled pods (see SandboxPodCacheOptions).
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(runForSandboxPod),
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

// runForSandboxPod maps a sandbox pod to the AgentRun it belongs to.
func runForSandboxPod(_ context.Context, obj client.Object) []reconcile.Request {
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
		if run.Status.Phase == konveyoriov1alpha1.AgentRunPhaseSucceeded ||
			run.Status.Phase == konveyoriov1alpha1.AgentRunPhaseFailed {
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
func sanitizeVolumeName(name string) string {
	name = strings.ToLower(name)
	name = volumeNameRegex.ReplaceAllString(name, "-")
	// Trim leading/trailing hyphens.
	name = strings.Trim(name, "-")
	if len(name) > 63 {
		// Truncate and append a short hash to avoid collisions.
		hash := sha256.Sum256([]byte(name))
		name = name[:54] + "-" + hex.EncodeToString(hash[:4])
	}
	return name
}
