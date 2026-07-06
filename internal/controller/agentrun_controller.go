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
	"encoding/hex"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

const (
	// secretKeyLength is the length of the generated ACP secret key in bytes.
	secretKeyLength = 32

	// workspaceVolumeName is the name of the EmptyDir volume for the agent workspace.
	workspaceVolumeName = "workspace"

	// sandboxFinishedReasonSucceeded is the Sandbox condition reason for success.
	sandboxFinishedReasonSucceeded = "Succeeded"

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

// Reconcile handles AgentRun reconciliation.
//
// The controller:
// 1. Checks that the referenced Agent exists and is Ready
// 2. Validates params and model selections against Agent declarations
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

	// Validate model selections against Agent's available providers.
	if err := r.validateModels(&run, &agent); err != nil {
		run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
		meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: run.Generation,
			Reason:             "InvalidModels",
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

	// Update AgentRun phase from Sandbox conditions.
	r.updatePhaseFromSandbox(&run, &sandbox)

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

// validateModels checks that model selections reference providers in the
// Agent's available set.
func (r *AgentRunReconciler) validateModels(
	run *konveyoriov1alpha1.AgentRun,
	agent *konveyoriov1alpha1.Agent,
) error {
	providerSet := make(map[string]bool)
	for _, p := range agent.Spec.Providers {
		providerSet[p.Ref] = true
	}
	for _, m := range run.Spec.Models {
		if !providerSet[m.Provider] {
			return fmt.Errorf("model selection references provider %q which is not in Agent %q providers",
				m.Provider, agent.Name)
		}
	}
	return nil
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
				labelManagedBy:         managedByLabel,
				"konveyor.io/agentrun": run.Name,
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
	env, err := r.buildEnvVars(ctx, run, agent, secretName)
	if err != nil {
		return "", fmt.Errorf("building env vars: %w", err)
	}

	// Resolve skill images for ImageVolumes.
	volumes, volumeMounts, err := r.resolveSkillVolumes(ctx, agent, run.Namespace)
	if err != nil {
		return "", fmt.Errorf("resolving skill volumes: %w", err)
	}

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

	// Create the Sandbox CR.
	serviceEnabled := true
	sandbox := &sandboxv1beta1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sandboxName,
			Namespace: run.Namespace,
			Labels: map[string]string{
				labelManagedBy:                managedByLabel,
				"app.kubernetes.io/component": "agent-sandbox",
				"konveyor.io/agentrun":        run.Name,
				"konveyor.io/agent":           agent.Name,
			},
		},
		Spec: sandboxv1beta1.SandboxSpec{
			PodTemplate: sandboxv1beta1.PodTemplate{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:         "agent",
							Image:        agent.Spec.Image,
							Env:          env,
							EnvFrom:      run.Spec.EnvFrom,
							VolumeMounts: volumeMounts,
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

// buildEnvVars constructs the full env var list for the Sandbox container.
func (r *AgentRunReconciler) buildEnvVars(
	ctx context.Context,
	run *konveyoriov1alpha1.AgentRun,
	agent *konveyoriov1alpha1.Agent,
	acpSecretName string,
) ([]corev1.EnvVar, error) {
	var env []corev1.EnvVar

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

	// ACP secret key.
	env = append(env, corev1.EnvVar{
		Name: "GOOSE_SERVER__SECRET_KEY",
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

	// Model selections and LLM credential mounting.
	for _, m := range run.Spec.Models {
		prefix := "KONVEYOR_MODEL_" + strings.ToUpper(m.Role) + "_"
		env = append(env,
			corev1.EnvVar{Name: prefix + "PROVIDER", Value: m.Provider},
			corev1.EnvVar{Name: prefix + "MODEL", Value: m.Model},
		)

		// Mount the LLM provider's credential Secret.
		var provider konveyoriov1alpha1.LLMProvider
		providerKey := types.NamespacedName{Namespace: run.Namespace, Name: m.Provider}
		if err := r.Get(ctx, providerKey, &provider); err != nil {
			return nil, fmt.Errorf("looking up LLMProvider %q for model role %q: %w",
				m.Provider, m.Role, err)
		}
		env = append(env, corev1.EnvVar{
			Name: prefix + "API_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: provider.Spec.CredentialRef.SecretName,
					},
					Key: provider.Spec.CredentialRef.Key,
				},
			},
		})
	}

	// Pass through user-specified env vars.
	env = append(env, run.Spec.Env...)

	return env, nil
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
		volName := "skill-" + name
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

// updatePhaseFromSandbox updates the AgentRun phase based on the Sandbox status.
func (r *AgentRunReconciler) updatePhaseFromSandbox(
	run *konveyoriov1alpha1.AgentRun,
	sandbox *sandboxv1beta1.Sandbox,
) {
	// Check Sandbox conditions for Finished state.
	for _, cond := range sandbox.Status.Conditions {
		if cond.Type == "Finished" && cond.Status == metav1.ConditionTrue {
			now := metav1.Now()
			run.Status.CompletionTime = &now
			if run.Status.StartTime != nil {
				duration := int64(now.Sub(run.Status.StartTime.Time).Seconds())
				run.Status.Duration = &duration
			}

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

	// Sandbox is still running.
	if run.Status.Phase != konveyoriov1alpha1.AgentRunPhaseRunning {
		run.Status.Phase = konveyoriov1alpha1.AgentRunPhaseRunning
		now := metav1.Now()
		run.Status.StartTime = &now
		meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: run.Generation,
			Reason:             "Running",
			Message:            "Agent is running",
		})
	}
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
		Named("agentrun").
		Complete(r)
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
