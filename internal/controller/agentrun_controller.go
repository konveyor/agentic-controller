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
	"path"
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

	// SkillLoaderImage runs the skill-loader init container. It is the
	// controller's own image, so an agent image is not required to carry the
	// harness binary and the source list written here is always read by a
	// loader of the same version.
	SkillLoaderImage string
}

// +kubebuilder:rbac:groups=konveyor.io,resources=agentruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=konveyor.io,resources=agentruns/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=konveyor.io,resources=agentruns/finalizers,verbs=update
// +kubebuilder:rbac:groups=agents.x-k8s.io,resources=sandboxes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update

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
			meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: run.Generation,
				Reason:             "AgentNotFound",
				Message:            fmt.Sprintf("Agent %q not found", run.Spec.AgentRef),
			})
			return r.patchRunStatus(ctx, &run, original)
		}
		return ctrl.Result{}, err
	}

	// Check that the Agent is Ready before proceeding.
	agentReady := meta.FindStatusCondition(agent.Status.Conditions, ConditionTypeReady)
	if agentReady == nil || agentReady.Status != metav1.ConditionTrue {
		meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: run.Generation,
			Reason:             "AgentNotReady",
			Message:            fmt.Sprintf("Agent %q is not Ready", run.Spec.AgentRef),
		})
		return r.patchRunStatus(ctx, &run, original)
	}

	// Validate params against Agent declarations.
	if err := r.validateParams(&run, &agent); err != nil {
		run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
		meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: run.Generation,
			Reason:             "InvalidParams",
			Message:            err.Error(),
		})
		return r.patchRunStatus(ctx, &run, original)
	}

	// Validate gateway selection against Agent's available gateways.
	if err := r.validateGateway(&run, &agent); err != nil {
		run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
		meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: run.Generation,
			Reason:             "InvalidGateway",
			Message:            err.Error(),
		})
		return r.patchRunStatus(ctx, &run, original)
	}

	// If no Sandbox exists yet, create one.
	if run.Status.SandboxName == "" {
		sandboxName, err := r.createSandbox(ctx, &run, &agent)
		if err != nil {
			logger.Error(err, "Failed to create Sandbox", "agentRun", run.Name, "agent", agent.Name)
			meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: run.Generation,
				Reason:             "SandboxCreationFailed",
				Message:            fmt.Sprintf("Failed to create Sandbox for Agent %q: %v", agent.Name, err),
			})
			// Patch status then return the error so controller-runtime
			// requeues with exponential backoff.
			if _, patchErr := r.patchRunStatus(ctx, &run, original); patchErr != nil {
				return ctrl.Result{}, patchErr
			}
			return ctrl.Result{}, err
		}
		run.Status.SandboxName = sandboxName
		run.Status.Phase = konveyoriov1alpha1.AgentRunPhasePending
		meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: run.Generation,
			Reason:             "SandboxCreated",
			Message:            fmt.Sprintf("Sandbox %q created", sandboxName),
		})
		return r.patchRunStatus(ctx, &run, original)
	}

	// Watch the Sandbox status.
	var sandbox sandboxv1beta1.Sandbox
	sandboxKey := types.NamespacedName{Namespace: run.Namespace, Name: run.Status.SandboxName}
	if err := r.Get(ctx, sandboxKey, &sandbox); err != nil {
		if errors.IsNotFound(err) {
			run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
			meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: run.Generation,
				Reason:             "SandboxNotFound",
				Message:            fmt.Sprintf("Sandbox %q was deleted", run.Status.SandboxName),
			})
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
	declared := make(map[string]konveyoriov1alpha1.AgentParam)
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

	return nil
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

	// Build env vars: KONVEYOR_PARAM_* from params + ACP secret key + LLM credentials.
	env, envFrom, err := r.buildEnvVars(ctx, run, agent, secretName)
	if err != nil {
		return "", fmt.Errorf("building env vars: %w", err)
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

	volumeMounts := make([]corev1.VolumeMount, 0, 3) // skills, workspace, tmp
	volumeMounts = append(volumeMounts, corev1.VolumeMount{
		Name:      skillsVolumeName,
		MountPath: skillsDir,
		ReadOnly:  true,
	})

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

// buildEnvVars constructs the env var list for the Sandbox container, plus
// envFrom sources for the gateway's credential Secret when it is exposed
// whole (credentialRef without a key, e.g. AWS SigV4).
func (r *AgentRunReconciler) buildEnvVars(
	ctx context.Context,
	run *konveyoriov1alpha1.AgentRun,
	agent *konveyoriov1alpha1.Agent,
	acpSecretName string,
) ([]corev1.EnvVar, []corev1.EnvFromSource, error) {
	var env []corev1.EnvVar
	var envFrom []corev1.EnvFromSource

	// Build KONVEYOR_PARAM_* env vars from params (supplied values
	// override defaults from the Agent).
	supplied := make(map[string]string)
	for _, p := range run.Spec.Params {
		supplied[p.Name] = p.Value
	}
	for _, p := range agent.Spec.Params {
		value, ok := supplied[p.Name]
		if !ok {
			value = p.Default
		}
		if value != "" {
			env = append(env, corev1.EnvVar{
				Name:  "KONVEYOR_PARAM_" + strings.ToUpper(p.Name),
				Value: value,
			})
		}
	}

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

	// Instructions (if any).
	if run.Spec.Instructions != "" {
		env = append(env, corev1.EnvVar{
			Name:  "KONVEYOR_INSTRUCTIONS",
			Value: run.Spec.Instructions,
		})
	}

	// Agent prompt.
	if agent.Spec.Prompt != "" {
		env = append(env, corev1.EnvVar{
			Name:  "KONVEYOR_PROMPT",
			Value: agent.Spec.Prompt,
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

			if cond.Reason == sandboxFinishedReasonSucceeded {
				run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseSucceeded
				meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
					Type:               ConditionTypeReady,
					Status:             metav1.ConditionTrue,
					ObservedGeneration: run.Generation,
					Reason:             sandboxFinishedReasonSucceeded,
					Message:            "Agent run completed successfully",
				})
			} else {
				run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
				meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
					Type:               ConditionTypeReady,
					Status:             metav1.ConditionFalse,
					ObservedGeneration: run.Generation,
					Reason:             "Failed",
					Message:            fmt.Sprintf("Sandbox finished with reason: %s", cond.Reason),
				})
			}
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
		meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: run.Generation,
			Reason:             "PodNotRunning",
			Message:            fmt.Sprintf("Waiting for sandbox pod %q to run (%s)", sandbox.Name, podPhase),
		})
		return
	}
	run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseRunning
	start := podStartTime(pod, sandbox.CreationTimestamp)
	run.Status.StartTime = &start
	meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
		Type:               ConditionTypeReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: run.Generation,
		Reason:             "Running",
		Message:            "Agent is running",
	})
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
