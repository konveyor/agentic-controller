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
	"maps"

	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

const (
	// workflowRunRefIndexField is the field index for looking up
	// AgentWorkflowRuns by workflowRef.
	workflowRunRefIndexField = ".spec.workflowRef"
)

// AgentWorkflowRunReconciler reconciles an AgentWorkflowRun object.
type AgentWorkflowRunReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// +kubebuilder:rbac:groups=konveyor.io,resources=agentworkflowruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=konveyor.io,resources=agentworkflowruns/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=konveyor.io,resources=agentworkflowruns/finalizers,verbs=update
// +kubebuilder:rbac:groups=konveyor.io,resources=agentworkflows,verbs=get;list;watch
// +kubebuilder:rbac:groups=konveyor.io,resources=agentruns,verbs=get;list;watch;create
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile handles AgentWorkflowRun reconciliation.
//
// The controller orchestrates sequential execution of workflow stages:
// 1. Looks up the referenced AgentWorkflow
// 2. Determines the current stage from status
// 3. Creates an AgentRun for the current stage if none exists
// 4. Watches the AgentRun to completion
// 5. Advances to the next stage or marks the workflow run as complete
func (r *AgentWorkflowRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var pbRun konveyoriov1alpha1.AgentWorkflowRun
	if err := r.Get(ctx, req.NamespacedName, &pbRun); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.V(1).Info("Reconciling AgentWorkflowRun", "name", pbRun.Name)

	original := pbRun.DeepCopy()
	pbRun.Status.ObservedGeneration = pbRun.Generation

	// If the run is already terminal, nothing to do.
	if pbRun.Status.Phase == konveyoriov1alpha1.AgentRunPhaseSucceeded ||
		pbRun.Status.Phase == konveyoriov1alpha1.AgentRunPhaseFailed {
		return ctrl.Result{}, nil
	}

	// Look up the referenced AgentWorkflow.
	var workflow konveyoriov1alpha1.AgentWorkflow
	workflowKey := types.NamespacedName{Namespace: pbRun.Namespace, Name: pbRun.Spec.WorkflowRef}
	if err := r.Get(ctx, workflowKey, &workflow); err != nil {
		if errors.IsNotFound(err) {
			pbRun.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
			now := metav1.Now()
			pbRun.Status.CompletionTime = &now
			meta.SetStatusCondition(&pbRun.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: pbRun.Generation,
				Reason:             "WorkflowNotFound",
				Message:            fmt.Sprintf("AgentWorkflow %q not found", pbRun.Spec.WorkflowRef),
			})
			return r.patchRunStatus(ctx, &pbRun, original)
		}
		return ctrl.Result{}, err
	}

	// Check that the workflow is Ready.
	workflowReady := meta.FindStatusCondition(workflow.Status.Conditions, ConditionTypeReady)
	if workflowReady == nil || workflowReady.Status != metav1.ConditionTrue {
		meta.SetStatusCondition(&pbRun.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: pbRun.Generation,
			Reason:             "WorkflowNotReady",
			Message:            fmt.Sprintf("AgentWorkflow %q is not Ready", pbRun.Spec.WorkflowRef),
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
		pbRun.Status.Stages = make([]konveyoriov1alpha1.AgentWorkflowRunStageStatus, len(workflow.Spec.Stages))
		for i, stage := range workflow.Spec.Stages {
			pbRun.Status.Stages[i] = konveyoriov1alpha1.AgentWorkflowRunStageStatus{
				Name:  stage.Name,
				Phase: konveyoriov1alpha1.AgentRunPhasePending,
			}
		}
	}

	// Find the current stage to process. Use the snapshotted status
	// stages as the source of truth — the workflow could have been
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

	// Look up the stage definition from the workflow by name
	// (matching the snapshotted status entry).
	stageStatus := &pbRun.Status.Stages[stageIndex]
	var stage *konveyoriov1alpha1.AgentWorkflowStage
	for i := range workflow.Spec.Stages {
		if workflow.Spec.Stages[i].Name == stageStatus.Name {
			stage = &workflow.Spec.Stages[i]
			break
		}
	}
	if stage == nil {
		// The workflow was modified and no longer has this stage.
		pbRun.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
		now := metav1.Now()
		pbRun.Status.CompletionTime = &now
		meta.SetStatusCondition(&pbRun.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: pbRun.Generation,
			Reason:             "StageNotFound",
			Message:            fmt.Sprintf("Stage %q no longer exists in AgentWorkflow %q", stageStatus.Name, pbRun.Spec.WorkflowRef),
		})
		return r.patchRunStatus(ctx, &pbRun, original)
	}

	pbRun.Status.CurrentStage = stage.Name
	pbRun.Status.Phase = konveyoriov1alpha1.AgentRunPhaseRunning

	// If no AgentRun exists for this stage, create one.
	if stageStatus.AgentRunName == "" {
		agentRunName, err := r.createAgentRunForStage(ctx, &pbRun, &workflow, stage, stageIndex, len(pbRun.Status.Stages))
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
		// Stage failed — fail the entire workflow run.
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
func (r *AgentWorkflowRunReconciler) findCurrentStageIndex(
	pbRun *konveyoriov1alpha1.AgentWorkflowRun,
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

// createAgentRunForStage creates an AgentRun for the given workflow stage.
// It forwards models, env, and envFrom from the workflow run spec. Params
// are filtered to only those the stage's Agent declares — this avoids
// forcing every stage Agent to declare every param from other stages.
// Workflow-level instructions (Guide) are passed as a separate env var
// so the harness can present them alongside stage instructions without
// the controller making prompt composition decisions.
//
// Uses a deterministic name (<workflowrun>-<stage>) so that duplicate
// creation on status-patch conflict is caught by AlreadyExists.
func (r *AgentWorkflowRunReconciler) createAgentRunForStage(
	ctx context.Context,
	pbRun *konveyoriov1alpha1.AgentWorkflowRun,
	workflow *konveyoriov1alpha1.AgentWorkflow,
	stage *konveyoriov1alpha1.AgentWorkflowStage,
	stageIndex int,
	stageCount int,
) (string, error) {
	agentRunName := stageAgentRunName(pbRun.Name, stage.Name)

	// Look up the stage's Agent to determine which params it declares.
	var agent konveyoriov1alpha1.Agent
	if err := r.Get(ctx, types.NamespacedName{
		Name: stage.AgentRef, Namespace: pbRun.Namespace,
	}, &agent); err != nil {
		return "", fmt.Errorf("looking up Agent %q for stage %q: %w", stage.AgentRef, stage.Name, err)
	}

	// Build a set of param names the stage Agent declares.
	declared := make(map[string]bool, len(agent.Spec.Params))
	for _, p := range agent.Spec.Params {
		declared[p.Name] = true
	}

	// Filter workflow-run params to only those this stage's Agent declares.
	// Params not declared by the stage Agent are silently dropped — log
	// and emit an event so typos are debuggable.
	var stageParams []konveyoriov1alpha1.AgentRunParam
	var skipped []string
	for _, p := range pbRun.Spec.Params {
		if declared[p.Name] {
			stageParams = append(stageParams, p)
		} else {
			skipped = append(skipped, p.Name)
		}
	}
	if len(skipped) > 0 {
		logger := log.FromContext(ctx)
		logger.V(1).Info("Filtered undeclared params for stage",
			"stage", stage.Name,
			"agent", stage.AgentRef,
			"skippedParams", skipped,
		)
		r.Recorder.Eventf(pbRun, nil, corev1.EventTypeNormal, "ParamsFiltered",
			"FilterParams", "Stage %q (Agent %q): skipped undeclared params: %s",
			stage.Name, stage.AgentRef, strings.Join(skipped, ", "))
	}

	// User-supplied env vars first, then controller-owned vars last.
	// Kubernetes uses last-entry-wins for duplicate names, so
	// controller-injected vars cannot be overridden by user input.
	var env []corev1.EnvVar
	env = append(env, pbRun.Spec.Env...)

	// Controller-owned env vars — appended after user env so they
	// cannot be spoofed.
	if workflow.Spec.Guide != "" {
		env = append(env, corev1.EnvVar{
			Name:  "KONVEYOR_WORKFLOW_GUIDE",
			Value: workflow.Spec.Guide,
		})
	}

	// Stage metadata for the harness. Used for stage-aware token
	// revocation: the harness revokes the Hub API token only on the
	// last stage (#68).
	env = append(env,
		corev1.EnvVar{
			Name:  "KONVEYOR_WORKFLOW_STAGE",
			Value: fmt.Sprintf("%d", stageIndex+1),
		},
		corev1.EnvVar{
			Name:  "KONVEYOR_WORKFLOW_STAGE_COUNT",
			Value: fmt.Sprintf("%d", stageCount),
		},
	)

	// Stage AgentRuns inherit all of the workflow run's labels so
	// label-selector queries (e.g. konveyor.io/application, ADR 0006)
	// match the runs that actually execute. Controller-owned keys are
	// written last into a copy so callers cannot override them and the
	// parent's live label map is never mutated.
	labels := make(map[string]string, len(pbRun.Labels)+3)
	maps.Copy(labels, pbRun.Labels)
	labels[labelManagedBy] = managedByLabel
	labels[labelAgentWorkflowRun] = pbRun.Name
	labels[labelStage] = stage.Name

	agentRun := &konveyoriov1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agentRunName,
			Namespace: pbRun.Namespace,
			Labels:    labels,
		},
		Spec: konveyoriov1alpha1.AgentRunSpec{
			AgentRef:     stage.AgentRef,
			Instructions: stage.Instructions,
			Gateway:      pbRun.Spec.Gateway,
			Params:       stageParams,
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
			// status patch failed. Verify it belongs to this workflow
			// run before accepting it.
			var existing konveyoriov1alpha1.AgentRun
			if getErr := r.Get(ctx, types.NamespacedName{
				Name: agentRunName, Namespace: pbRun.Namespace,
			}, &existing); getErr != nil {
				return "", fmt.Errorf("fetching existing AgentRun %q: %w", agentRunName, getErr)
			}
			if !isOwnedBy(&existing, pbRun) {
				return "", fmt.Errorf("AgentRun %q already exists but is not owned by this workflow run", agentRunName)
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

// patchRunStatus patches the AgentWorkflowRun status.
func (r *AgentWorkflowRunReconciler) patchRunStatus(
	ctx context.Context,
	pbRun *konveyoriov1alpha1.AgentWorkflowRun,
	original *konveyoriov1alpha1.AgentWorkflowRun,
) (ctrl.Result, error) {
	if err := r.Status().Patch(ctx, pbRun, client.MergeFrom(original)); err != nil {
		log.FromContext(ctx).Error(err, "Failed to patch AgentWorkflowRun status",
			"agentWorkflowRun", pbRun.Name)
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AgentWorkflowRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Index AgentWorkflowRuns by workflowRef for efficient reverse lookup
	// when an AgentWorkflow changes.
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&konveyoriov1alpha1.AgentWorkflowRun{},
		workflowRunRefIndexField,
		func(obj client.Object) []string {
			pbRun := obj.(*konveyoriov1alpha1.AgentWorkflowRun)
			return []string{pbRun.Spec.WorkflowRef}
		},
	); err != nil {
		return fmt.Errorf("indexing %s: %w", workflowRunRefIndexField, err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&konveyoriov1alpha1.AgentWorkflowRun{}).
		Owns(&konveyoriov1alpha1.AgentRun{}).
		Watches(
			&konveyoriov1alpha1.AgentWorkflow{},
			handler.EnqueueRequestsFromMapFunc(r.findRunsForWorkflow),
		).
		Named("agentworkflowrun").
		Complete(r)
}

// findRunsForWorkflow returns reconcile requests for all non-terminal
// AgentWorkflowRuns that reference the given AgentWorkflow.
func (r *AgentWorkflowRunReconciler) findRunsForWorkflow(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	workflow, ok := obj.(*konveyoriov1alpha1.AgentWorkflow)
	if !ok {
		return nil
	}

	var runList konveyoriov1alpha1.AgentWorkflowRunList
	if err := r.List(ctx, &runList,
		client.InNamespace(workflow.Namespace),
		client.MatchingFields{workflowRunRefIndexField: workflow.Name},
	); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list AgentWorkflowRuns for AgentWorkflow",
			"workflow", workflow.Name)
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
