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
	stderrors "errors"
	"fmt"
	"maps"
	"time"

	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
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

	// DefaultTTLAfterFinished, when non-nil, is the fallback lifetime applied
	// to a terminal AgentWorkflowRun whose spec does not set
	// TTLSecondsAfterFinished. The controller deletes such a run this long
	// after it finished — cascading to the child AgentRuns it owns. Nil
	// disables the default. A run's own spec.ttlSecondsAfterFinished always
	// overrides this. Wired from the --agentrun-ttl flag in main.
	DefaultTTLAfterFinished *time.Duration

	// apiReader confirms a cache miss before the orphan sweep deletes child
	// AgentRuns. Parent and child informer events are not ordered across types.
	// Set by SetupWithManager.
	apiReader client.Reader
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
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		if r.apiReader != nil {
			var current konveyoriov1alpha1.AgentWorkflowRun
			if err := r.apiReader.Get(ctx, req.NamespacedName, &current); err == nil {
				return ctrl.Result{Requeue: true}, nil
			} else if !errors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, r.cleanupOrphanedWorkflowRunResources(ctx, req.NamespacedName)
	}

	logger.V(1).Info("Reconciling AgentWorkflowRun", "name", pbRun.Name)

	original := pbRun.DeepCopy()
	pbRun.Status.ObservedGeneration = pbRun.Generation

	// A terminal run has no orchestration work left; the only remaining
	// action is TTL-based garbage collection.
	if isTerminalPhase(pbRun.Status.Phase) {
		return r.reconcileTTL(ctx, &pbRun, original)
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

	// Initialize the run from a snapshot of the workflow definition
	// (#87). The controller executes from this snapshot, not the live
	// workflow spec, so a mid-run edit to the workflow (stage agent,
	// instructions, execution, guide, or params) cannot change stages
	// that have already been planned.
	if len(pbRun.Status.Stages) == 0 {
		pbRun.Status.Guide = workflow.Spec.Guide
		pbRun.Status.Params = workflow.Spec.Params
		pbRun.Status.Stages = make([]konveyoriov1alpha1.AgentWorkflowRunStageStatus, len(workflow.Spec.Stages))
		for i, stage := range workflow.Spec.Stages {
			pbRun.Status.Stages[i] = konveyoriov1alpha1.AgentWorkflowRunStageStatus{
				Name:         stage.Name,
				Phase:        konveyoriov1alpha1.AgentRunPhasePending,
				AgentRef:     stage.AgentRef,
				Instructions: stage.Instructions,
				Execution:    stage.Execution,
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

	// Read the stage definition from the snapshot captured at init
	// (#87), not the live workflow — so a mid-run edit cannot change a
	// planned stage. Reconstruct the stage from the snapshotted fields.
	stageStatus := &pbRun.Status.Stages[stageIndex]
	stage := &konveyoriov1alpha1.AgentWorkflowStage{
		Name:         stageStatus.Name,
		AgentRef:     stageStatus.AgentRef,
		Instructions: stageStatus.Instructions,
		Execution:    stageStatus.Execution,
	}

	pbRun.Status.CurrentStage = stage.Name
	pbRun.Status.Phase = konveyoriov1alpha1.AgentRunPhaseRunning

	// If no AgentRun exists for this stage, create one.
	if stageStatus.AgentRunName == "" {
		agentRunName, err := r.createAgentRunForStage(ctx, &pbRun, stage, stageIndex, len(pbRun.Status.Stages))
		if err != nil {
			// A permanent config error (bad workflow param, unresolved
			// $(workflow.<name>) in the guide) can never clear on retry —
			// fail the run terminally, matching the AgentRun's
			// InvalidParams. Everything else is transient: requeue.
			var cfgErr *configError
			if stderrors.As(err, &cfgErr) {
				stageStatus.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
				pbRun.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
				now := metav1.Now()
				pbRun.Status.CompletionTime = &now
				meta.SetStatusCondition(&pbRun.Status.Conditions, metav1.Condition{
					Type:               ConditionTypeReady,
					Status:             metav1.ConditionFalse,
					ObservedGeneration: pbRun.Generation,
					Reason:             "InvalidParams",
					Message:            fmt.Sprintf("Stage %q: %v", stage.Name, err),
				})
				return r.patchRunStatus(ctx, &pbRun, original)
			}

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

	// Mirror the AgentRun's phase onto the stage status for display.
	stageStatus.Phase = agentRun.Status.Phase

	// Sequence on the AgentRun's Succeeded condition, not phase (ADR 0018):
	// True advances to the next stage, False (failure or limit reached)
	// stops the workflow, Unknown/absent keeps waiting. This is the
	// controller state machine reading Succeeded so phase can eventually
	// be retired.
	succeeded := meta.FindStatusCondition(agentRun.Status.Conditions,
		konveyoriov1alpha1.AgentRunConditionSucceeded)
	switch {
	case succeeded != nil && succeeded.Status == metav1.ConditionTrue:
		// Stage completed — the next reconcile will advance to the next stage.
		meta.SetStatusCondition(&pbRun.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: pbRun.Generation,
			Reason:             "StageSucceeded",
			Message:            fmt.Sprintf("Stage %q completed successfully", stage.Name),
		})
		return r.patchRunStatus(ctx, &pbRun, original)

	case succeeded != nil && succeeded.Status == metav1.ConditionFalse:
		// Stage did not succeed (failure or limit reached) — fail the
		// entire workflow run.
		pbRun.Status.Phase = konveyoriov1alpha1.AgentRunPhaseFailed
		now := metav1.Now()
		pbRun.Status.CompletionTime = &now
		// Surface the child AgentRun's full message, not just its Reason: the
		// child is immutable and controller-managed, so its own status is a
		// dead end for the user. The actionable guidance (e.g. "select one via
		// spec.gateway") only reaches an editable resource here -- and because
		// spec.gateway on the AgentWorkflowRun is what feeds the stage, that
		// guidance reads correctly against this resource. Fall back to Reason
		// when the child left no message.
		detail := succeeded.Message
		if detail == "" {
			detail = succeeded.Reason
		}
		meta.SetStatusCondition(&pbRun.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: pbRun.Generation,
			Reason:             "StageFailed",
			Message:            fmt.Sprintf("Stage %q did not succeed: %s", stage.Name, detail),
		})
		return r.patchRunStatus(ctx, &pbRun, original)

	default:
		// Stage is still running (Succeeded=Unknown or not yet set).
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
// configError marks a permanent configuration error in a workflow run —
// a bad workflow param value or an unresolved $(workflow.<name>) in the
// guide. The declarations come from the frozen Status snapshot and the
// supplied values from the immutable spec, so retrying cannot clear it;
// the caller fails the run terminally (InvalidParams) rather than
// requeuing forever, matching the AgentRun side.
type configError struct{ err error }

func (e *configError) Error() string { return e.err.Error() }
func (e *configError) Unwrap() error { return e.err }

func (r *AgentWorkflowRunReconciler) createAgentRunForStage(
	ctx context.Context,
	pbRun *konveyoriov1alpha1.AgentWorkflowRun,
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
	// Workflow-declared params (snapshotted at init, #87) are legitimate
	// even when a stage's Agent does not declare them — they feed the
	// workflow section of params.json and $(workflow.<name>) in the guide.
	workflowDeclared := make(map[string]bool, len(pbRun.Status.Params))
	for _, p := range pbRun.Status.Params {
		workflowDeclared[p.Name] = true
	}

	// Pass only the stage Agent's declared params through to its AgentRun.
	// A param declared by neither the stage Agent nor the workflow is a
	// typo — log and emit an event so it is debuggable; a workflow-only
	// param (used in the guide) is not skipped, it is consumed below.
	var stageParams []konveyoriov1alpha1.ParamValue
	var skipped []string
	for _, p := range pbRun.Spec.Params {
		if declared[p.Name] {
			stageParams = append(stageParams, p)
		} else if !workflowDeclared[p.Name] {
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
	// Resolve workflow-level params once from the snapshot (#87): coerce
	// for stamping onto the stage AgentRun (params.json workflow
	// section) and keep the string form for substituting the guide
	// (workflow scope only — the guide is ambient across stages and must
	// not reference stage-agent params). ADR 0009/0018.
	workflowRaw, workflowStrs, err := coerceWorkflowParams(
		pbRun.Status.Params, suppliedValues(pbRun.Spec.Params))
	if err != nil {
		// Permanent: declarations are the frozen Status.Params snapshot and
		// the supplied values are on the immutable spec, so retrying cannot
		// clear it. Fail terminally, matching the AgentRun's InvalidParams.
		return "", &configError{fmt.Errorf("resolving workflow params: %w", err)}
	}

	if pbRun.Status.Guide != "" {
		guide, err := substitute(pbRun.Status.Guide, map[string]map[string]string{scopeWorkflow: workflowStrs})
		if err != nil {
			// Permanent: the guide is a frozen snapshot; an unresolved
			// $(workflow.<name>) never clears on retry.
			return "", &configError{fmt.Errorf("workflow guide: %w", err)}
		}
		env = append(env, corev1.EnvVar{
			Name:  "KONVEYOR_WORKFLOW_GUIDE",
			Value: guide,
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
			// Stamp the stage-resolved execution config (stage override,
			// else Agent default) onto the stage's AgentRun so the
			// AgentRun controller — which cannot see the workflow — resolves
			// and delivers it uniformly (ADR 0018).
			Execution: resolveExecution(stage.Execution, agent.Spec.Execution),
			// Stamp coerced workflow params so the AgentRun controller can
			// write the params.json workflow section and substitute
			// $(workflow.<name>) without seeing the AgentWorkflow (ADR 0018).
			WorkflowParams: workflowRaw,
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

// effectiveTTL resolves a terminal AgentWorkflowRun's lifetime: the run's own
// spec.ttlSecondsAfterFinished wins, then the controller's
// DefaultTTLAfterFinished. The bool is false when neither is set, meaning GC
// is disabled and the run is kept until deleted manually.
func (r *AgentWorkflowRunReconciler) effectiveTTL(pbRun *konveyoriov1alpha1.AgentWorkflowRun) (time.Duration, bool) {
	if pbRun.Spec.TTLSecondsAfterFinished != nil {
		return time.Duration(*pbRun.Spec.TTLSecondsAfterFinished) * time.Second, true
	}
	if r.DefaultTTLAfterFinished != nil {
		return *r.DefaultTTLAfterFinished, true
	}
	return 0, false
}

// reconcileTTL garbage-collects a terminal AgentWorkflowRun once its TTL
// elapses. With no effective TTL the run is kept. The finish anchor is
// CompletionTime; a terminal run that never recorded one gets it stamped now
// so expiry is deterministic across controller restarts. Before the TTL
// elapses the reconcile is requeued for the remaining time; once it has, the
// run is deleted — cascading to the child AgentRuns it owns via owner
// references.
func (r *AgentWorkflowRunReconciler) reconcileTTL(
	ctx context.Context,
	pbRun *konveyoriov1alpha1.AgentWorkflowRun,
	original *konveyoriov1alpha1.AgentWorkflowRun,
) (ctrl.Result, error) {
	ttl, ok := r.effectiveTTL(pbRun)
	if !ok {
		return ctrl.Result{}, nil
	}

	// Anchor the clock: a terminal run with no CompletionTime records one now.
	if pbRun.Status.CompletionTime == nil {
		now := metav1.Now()
		pbRun.Status.CompletionTime = &now
		return r.patchRunStatus(ctx, pbRun, original)
	}

	if remaining := time.Until(pbRun.Status.CompletionTime.Add(ttl)); remaining > 0 {
		return ctrl.Result{RequeueAfter: remaining}, nil
	}

	log.FromContext(ctx).Info("Deleting finished AgentWorkflowRun (TTL elapsed)",
		"agentWorkflowRun", pbRun.Name, "ttl", ttl.String(), "completionTime", pbRun.Status.CompletionTime)
	if err := r.Delete(ctx, pbRun); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AgentWorkflowRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.apiReader = mgr.GetAPIReader()

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
		// The owner watch remains authoritative. This label map is only the
		// recovery source for an ownerless stage AgentRun.
		Watches(
			&konveyoriov1alpha1.AgentRun{},
			handler.EnqueueRequestsFromMapFunc(workflowRunForLabeledResource),
			builder.WithPredicates(predicate.NewPredicateFuncs(isOwnerlessManagedWorkflowRunResource)),
		).
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
