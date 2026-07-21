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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

const (
	// playbookRunRefIndexField is the field index for looking up
	// AgentPlaybookRuns by playbookRef.
	playbookRunRefIndexField = ".spec.playbookRef"
)

// AgentPlaybookRunReconciler reconciles an AgentPlaybookRun object.
type AgentPlaybookRunReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=konveyor.io,resources=agentplaybookruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=konveyor.io,resources=agentplaybookruns/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=konveyor.io,resources=agentplaybookruns/finalizers,verbs=update
// +kubebuilder:rbac:groups=konveyor.io,resources=agentplaybooks,verbs=get;list;watch
// +kubebuilder:rbac:groups=konveyor.io,resources=agentruns,verbs=get;list;watch;create

// Reconcile handles AgentPlaybookRun reconciliation.
//
// The controller orchestrates sequential execution of playbook stages:
// 1. Looks up the referenced AgentPlaybook
// 2. Determines the current stage from status
// 3. Creates an AgentRun for the current stage if none exists
// 4. Watches the AgentRun to completion
// 5. Advances to the next stage or marks the playbook run as complete
func (r *AgentPlaybookRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var pbRun konveyoriov1alpha1.AgentPlaybookRun
	if err := r.Get(ctx, req.NamespacedName, &pbRun); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.V(1).Info("Reconciling AgentPlaybookRun", "name", pbRun.Name)

	original := pbRun.DeepCopy()
	pbRun.Status.ObservedGeneration = pbRun.Generation

	// If the run is already terminal, nothing to do.
	if pbRun.Status.Phase == konveyoriov1alpha1.AgentRunPhaseSucceeded ||
		pbRun.Status.Phase == konveyoriov1alpha1.AgentRunPhaseFailed {
		return ctrl.Result{}, nil
	}

	// Look up the referenced AgentPlaybook.
	var playbook konveyoriov1alpha1.AgentPlaybook
	playbookKey := types.NamespacedName{Namespace: pbRun.Namespace, Name: pbRun.Spec.PlaybookRef}
	if err := r.Get(ctx, playbookKey, &playbook); err != nil {
		if errors.IsNotFound(err) {
			pbRun.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
			now := metav1.Now()
			pbRun.Status.CompletionTime = &now
			meta.SetStatusCondition(&pbRun.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: pbRun.Generation,
				Reason:             "PlaybookNotFound",
				Message:            fmt.Sprintf("AgentPlaybook %q not found", pbRun.Spec.PlaybookRef),
			})
			return r.patchRunStatus(ctx, &pbRun, original)
		}
		return ctrl.Result{}, err
	}

	// Check that the playbook is Ready.
	playbookReady := meta.FindStatusCondition(playbook.Status.Conditions, ConditionTypeReady)
	if playbookReady == nil || playbookReady.Status != metav1.ConditionTrue {
		meta.SetStatusCondition(&pbRun.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: pbRun.Generation,
			Reason:             "PlaybookNotReady",
			Message:            fmt.Sprintf("AgentPlaybook %q is not Ready", pbRun.Spec.PlaybookRef),
		})
		return r.patchRunStatus(ctx, &pbRun, original)
	}

	// Set start time on first reconcile.
	if pbRun.Status.StartTime == nil {
		now := metav1.Now()
		pbRun.Status.StartTime = &now
		pbRun.Status.Phase = konveyoriov1alpha1.AgentRunPhasePending
	}

	// Initialize stage statuses if empty.
	if len(pbRun.Status.Stages) == 0 {
		pbRun.Status.Stages = make([]konveyoriov1alpha1.AgentPlaybookRunStageStatus, len(playbook.Spec.Stages))
		for i, stage := range playbook.Spec.Stages {
			pbRun.Status.Stages[i] = konveyoriov1alpha1.AgentPlaybookRunStageStatus{
				Name:  stage.Name,
				Phase: konveyoriov1alpha1.AgentRunPhasePending,
			}
		}
	}

	// Find the current stage to process. Use the snapshotted status
	// stages as the source of truth — the playbook could have been
	// modified since the run started, but the run executes the stages
	// that were captured at initialization time.
	stageIndex := r.findCurrentStageIndex(&pbRun)
	if stageIndex >= len(pbRun.Status.Stages) {
		// All stages completed successfully.
		pbRun.Status.Phase = konveyoriov1alpha1.AgentRunPhaseSucceeded
		pbRun.Status.CurrentStage = ""
		now := metav1.Now()
		pbRun.Status.CompletionTime = &now
		meta.SetStatusCondition(&pbRun.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: pbRun.Generation,
			Reason:             reasonSucceeded,
			Message:            "All stages completed successfully",
		})
		return r.patchRunStatus(ctx, &pbRun, original)
	}

	// Look up the stage definition from the playbook by name
	// (matching the snapshotted status entry).
	stageStatus := &pbRun.Status.Stages[stageIndex]
	var stage *konveyoriov1alpha1.AgentPlaybookStage
	for i := range playbook.Spec.Stages {
		if playbook.Spec.Stages[i].Name == stageStatus.Name {
			stage = &playbook.Spec.Stages[i]
			break
		}
	}
	if stage == nil {
		// The playbook was modified and no longer has this stage.
		pbRun.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
		now := metav1.Now()
		pbRun.Status.CompletionTime = &now
		meta.SetStatusCondition(&pbRun.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: pbRun.Generation,
			Reason:             "StageNotFound",
			Message:            fmt.Sprintf("Stage %q no longer exists in AgentPlaybook %q", stageStatus.Name, pbRun.Spec.PlaybookRef),
		})
		return r.patchRunStatus(ctx, &pbRun, original)
	}

	pbRun.Status.CurrentStage = stage.Name
	pbRun.Status.Phase = konveyoriov1alpha1.AgentRunPhaseRunning

	// If no AgentRun exists for this stage, create one.
	if stageStatus.AgentRunName == "" {
		agentRunName, err := r.createAgentRunForStage(ctx, &pbRun, &playbook, stage)
		if err != nil {
			logger.Error(err, "Failed to create AgentRun for stage",
				"stage", stage.Name)
			meta.SetStatusCondition(&pbRun.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: pbRun.Generation,
				Reason:             "AgentRunCreationFailed",
				Message:            fmt.Sprintf("Failed to create AgentRun for stage %q: %v", stage.Name, err),
			})
			if _, patchErr := r.patchRunStatus(ctx, &pbRun, original); patchErr != nil {
				return ctrl.Result{}, patchErr
			}
			return ctrl.Result{}, err
		}
		stageStatus.AgentRunName = agentRunName
		stageStatus.Phase = konveyoriov1alpha1.AgentRunPhasePending
		meta.SetStatusCondition(&pbRun.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: pbRun.Generation,
			Reason:             "StageRunning",
			Message:            fmt.Sprintf("Stage %q: AgentRun %q created", stage.Name, agentRunName),
		})
		return r.patchRunStatus(ctx, &pbRun, original)
	}

	// An AgentRun exists for this stage — check its status.
	var agentRun konveyoriov1alpha1.AgentRun
	agentRunKey := types.NamespacedName{Namespace: pbRun.Namespace, Name: stageStatus.AgentRunName}
	if err := r.Get(ctx, agentRunKey, &agentRun); err != nil {
		if errors.IsNotFound(err) {
			// The AgentRun was deleted externally — fail the stage.
			stageStatus.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
			pbRun.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
			now := metav1.Now()
			pbRun.Status.CompletionTime = &now
			meta.SetStatusCondition(&pbRun.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: pbRun.Generation,
				Reason:             "AgentRunDeleted",
				Message:            fmt.Sprintf("Stage %q: AgentRun %q was deleted", stage.Name, stageStatus.AgentRunName),
			})
			return r.patchRunStatus(ctx, &pbRun, original)
		}
		return ctrl.Result{}, err
	}

	// Mirror the AgentRun's phase onto the stage status.
	stageStatus.Phase = agentRun.Status.Phase

	switch agentRun.Status.Phase {
	case konveyoriov1alpha1.AgentRunPhaseSucceeded:
		// Stage completed — the next reconcile will advance to the next stage.
		meta.SetStatusCondition(&pbRun.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: pbRun.Generation,
			Reason:             "StageSucceeded",
			Message:            fmt.Sprintf("Stage %q completed successfully", stage.Name),
		})
		return r.patchRunStatus(ctx, &pbRun, original)

	case konveyoriov1alpha1.AgentRunPhaseFailed:
		// Stage failed — fail the entire playbook run.
		pbRun.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
		now := metav1.Now()
		pbRun.Status.CompletionTime = &now
		meta.SetStatusCondition(&pbRun.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: pbRun.Generation,
			Reason:             "StageFailed",
			Message:            fmt.Sprintf("Stage %q failed", stage.Name),
		})
		return r.patchRunStatus(ctx, &pbRun, original)

	default:
		// Stage is still running (Pending or Running).
		meta.SetStatusCondition(&pbRun.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: pbRun.Generation,
			Reason:             "StageRunning",
			Message:            fmt.Sprintf("Stage %q is %s", stage.Name, agentRun.Status.Phase),
		})
		return r.patchRunStatus(ctx, &pbRun, original)
	}
}

// findCurrentStageIndex returns the index of the first stage that has not
// yet succeeded. Returns len(stages) if all stages have succeeded.
func (r *AgentPlaybookRunReconciler) findCurrentStageIndex(
	pbRun *konveyoriov1alpha1.AgentPlaybookRun,
) int {
	for i, stage := range pbRun.Status.Stages {
		if stage.Phase != konveyoriov1alpha1.AgentRunPhaseSucceeded {
			return i
		}
	}
	return len(pbRun.Status.Stages)
}

// stageAgentRunName returns the deterministic name for a stage's AgentRun.
// Follows the Tekton pattern: <parent>-<child>, truncated to 63 chars
// with a hash suffix to avoid collisions.
func stageAgentRunName(pbRunName, stageName string) string {
	return sanitizeVolumeName(pbRunName + "-" + stageName)
}

// createAgentRunForStage creates an AgentRun for the given playbook stage.
// It forwards params, models, env, and envFrom from the playbook run spec.
// Playbook-level instructions (Guide) are passed as a separate env var
// so the harness can present them alongside stage instructions without
// the controller making prompt composition decisions.
//
// Uses a deterministic name (<playbookrun>-<stage>) so that duplicate
// creation on status-patch conflict is caught by AlreadyExists.
func (r *AgentPlaybookRunReconciler) createAgentRunForStage(
	ctx context.Context,
	pbRun *konveyoriov1alpha1.AgentPlaybookRun,
	playbook *konveyoriov1alpha1.AgentPlaybook,
	stage *konveyoriov1alpha1.AgentPlaybookStage,
) (string, error) {
	agentRunName := stageAgentRunName(pbRun.Name, stage.Name)

	// Pass playbook-level instructions (Guide) as an env var.
	// The harness decides how to compose this with the Agent prompt
	// and stage instructions.
	var env []corev1.EnvVar
	if playbook.Spec.Guide != "" {
		env = append(env, corev1.EnvVar{
			Name:  "KONVEYOR_PLAYBOOK_INSTRUCTIONS",
			Value: playbook.Spec.Guide,
		})
	}
	env = append(env, pbRun.Spec.Env...)

	agentRun := &konveyoriov1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agentRunName,
			Namespace: pbRun.Namespace,
			Labels: map[string]string{
				labelManagedBy:        managedByLabel,
				labelAgentPlaybookRun: pbRun.Name,
				labelStage:            stage.Name,
			},
		},
		Spec: konveyoriov1alpha1.AgentRunSpec{
			AgentRef:     stage.AgentRef,
			Instructions: stage.Instructions,
			Models:       pbRun.Spec.Models,
			Params:       pbRun.Spec.Params,
			Env:          env,
			EnvFrom:      pbRun.Spec.EnvFrom,
		},
	}

	if err := ctrl.SetControllerReference(pbRun, agentRun, r.Scheme); err != nil {
		return "", fmt.Errorf("setting AgentRun owner reference: %w", err)
	}

	if err := r.Create(ctx, agentRun); err != nil {
		if errors.IsAlreadyExists(err) {
			// AgentRun was likely created on a prior reconcile but the
			// status patch failed. Verify it belongs to this playbook
			// run before accepting it.
			var existing konveyoriov1alpha1.AgentRun
			if getErr := r.Get(ctx, types.NamespacedName{
				Name: agentRunName, Namespace: pbRun.Namespace,
			}, &existing); getErr != nil {
				return "", fmt.Errorf("fetching existing AgentRun %q: %w", agentRunName, getErr)
			}
			if !isOwnedBy(&existing, pbRun) {
				return "", fmt.Errorf("AgentRun %q already exists but is not owned by this playbook run", agentRunName)
			}
			return agentRunName, nil
		}
		return "", fmt.Errorf("creating AgentRun for stage %q: %w", stage.Name, err)
	}

	return agentRunName, nil
}

// isOwnedBy checks whether the child resource has a controller owner
// reference pointing to the expected parent.
func isOwnedBy(child client.Object, parent client.Object) bool {
	for _, ref := range child.GetOwnerReferences() {
		if ref.Controller != nil && *ref.Controller && ref.UID == parent.GetUID() {
			return true
		}
	}
	return false
}

// patchRunStatus patches the AgentPlaybookRun status.
func (r *AgentPlaybookRunReconciler) patchRunStatus(
	ctx context.Context,
	pbRun *konveyoriov1alpha1.AgentPlaybookRun,
	original *konveyoriov1alpha1.AgentPlaybookRun,
) (ctrl.Result, error) {
	if err := r.Status().Patch(ctx, pbRun, client.MergeFrom(original)); err != nil {
		log.FromContext(ctx).Error(err, "Failed to patch AgentPlaybookRun status",
			"agentPlaybookRun", pbRun.Name)
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AgentPlaybookRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Index AgentPlaybookRuns by playbookRef for efficient reverse lookup
	// when an AgentPlaybook changes.
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&konveyoriov1alpha1.AgentPlaybookRun{},
		playbookRunRefIndexField,
		func(obj client.Object) []string {
			pbRun := obj.(*konveyoriov1alpha1.AgentPlaybookRun)
			return []string{pbRun.Spec.PlaybookRef}
		},
	); err != nil {
		return fmt.Errorf("indexing %s: %w", playbookRunRefIndexField, err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&konveyoriov1alpha1.AgentPlaybookRun{}).
		Owns(&konveyoriov1alpha1.AgentRun{}).
		Watches(
			&konveyoriov1alpha1.AgentPlaybook{},
			handler.EnqueueRequestsFromMapFunc(r.findRunsForPlaybook),
		).
		Named("agentplaybookrun").
		Complete(r)
}

// findRunsForPlaybook returns reconcile requests for all non-terminal
// AgentPlaybookRuns that reference the given AgentPlaybook.
func (r *AgentPlaybookRunReconciler) findRunsForPlaybook(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	playbook, ok := obj.(*konveyoriov1alpha1.AgentPlaybook)
	if !ok {
		return nil
	}

	var runList konveyoriov1alpha1.AgentPlaybookRunList
	if err := r.List(ctx, &runList,
		client.InNamespace(playbook.Namespace),
		client.MatchingFields{playbookRunRefIndexField: playbook.Name},
	); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list AgentPlaybookRuns for AgentPlaybook",
			"playbook", playbook.Name)
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
