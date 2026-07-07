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
	"strings"

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
	// Field index keys for Agent -> referenced resource lookups.
	agentProviderRefIndexField        = ".spec.providers.ref"
	agentSkillCardRefIndexField       = ".spec.skillCards.ref"
	agentSkillCollectionRefIndexField = ".spec.skillCollections.ref"
)

// AgentReconciler reconciles an Agent object.
type AgentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=konveyor.io,resources=agents,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=konveyor.io,resources=agents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=konveyor.io,resources=agents/finalizers,verbs=update

// Reconcile handles Agent reconciliation.
//
// The controller validates that all referenced resources (LLMProviders,
// SkillCards, SkillCollections) exist and are Ready, then reports
// aggregate readiness on the Agent.
func (r *AgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var agent konveyoriov1alpha1.Agent
	if err := r.Get(ctx, req.NamespacedName, &agent); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.V(1).Info("Reconciling Agent", "name", agent.Name)

	original := agent.DeepCopy()
	agent.Status.ObservedGeneration = agent.Generation

	var notReadyReasons []string

	// Check LLMProviders.
	for _, provRef := range agent.Spec.Providers {
		ready, reason := r.checkRef(ctx, agent.Namespace, provRef.Ref,
			&konveyoriov1alpha1.LLMProvider{}, "LLMProvider")
		if !ready {
			notReadyReasons = append(notReadyReasons, reason)
		}
	}

	// Check SkillCards.
	for _, scRef := range agent.Spec.SkillCards {
		ready, reason := r.checkRef(ctx, agent.Namespace, scRef.Ref,
			&konveyoriov1alpha1.SkillCard{}, "SkillCard")
		if !ready {
			notReadyReasons = append(notReadyReasons, reason)
		}
	}

	// Check SkillCollections.
	for _, scolRef := range agent.Spec.SkillCollections {
		ready, reason := r.checkRef(ctx, agent.Namespace, scolRef.Ref,
			&konveyoriov1alpha1.SkillCollection{}, "SkillCollection")
		if !ready {
			notReadyReasons = append(notReadyReasons, reason)
		}
	}

	if len(notReadyReasons) == 0 {
		meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: agent.Generation,
			Reason:             "AllDependenciesReady",
			Message:            "All referenced providers, skills, and collections are ready",
		})
	} else {
		meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: agent.Generation,
			Reason:             "DependenciesNotReady",
			Message:            strings.Join(notReadyReasons, "; "),
		})
	}

	if err := r.Status().Patch(ctx, &agent, client.MergeFrom(original)); err != nil {
		logger.Error(err, "Failed to patch Agent status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// checkRef looks up a referenced resource by name and checks its Ready condition.
func (r *AgentReconciler) checkRef(
	ctx context.Context,
	namespace, name string,
	obj client.Object,
	kind string,
) (bool, string) {
	key := types.NamespacedName{Namespace: namespace, Name: name}
	if err := r.Get(ctx, key, obj); err != nil {
		if errors.IsNotFound(err) {
			return false, fmt.Sprintf("%s %q not found", kind, name)
		}
		return false, fmt.Sprintf("error fetching %s %q: %v", kind, name, err)
	}

	// Use type switch for the known types.
	var conditions []metav1.Condition
	switch o := obj.(type) {
	case *konveyoriov1alpha1.LLMProvider:
		conditions = o.Status.Conditions
	case *konveyoriov1alpha1.SkillCard:
		conditions = o.Status.Conditions
	case *konveyoriov1alpha1.SkillCollection:
		conditions = o.Status.Conditions
	default:
		return false, fmt.Sprintf("%s %q: unable to check readiness (unknown type)", kind, name)
	}

	readyCond := meta.FindStatusCondition(conditions, ConditionTypeReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionTrue {
		return false, fmt.Sprintf("%s %q is not Ready", kind, name)
	}

	return true, ""
}

// SetupWithManager sets up the controller with the Manager.
func (r *AgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()

	// Set up field indexes for efficient reverse lookups.
	if err := mgr.GetFieldIndexer().IndexField(ctx,
		&konveyoriov1alpha1.Agent{}, agentProviderRefIndexField,
		func(obj client.Object) []string {
			agent := obj.(*konveyoriov1alpha1.Agent)
			refs := make([]string, len(agent.Spec.Providers))
			for i, p := range agent.Spec.Providers {
				refs[i] = p.Ref
			}
			return refs
		},
	); err != nil {
		return fmt.Errorf("indexing %s: %w", agentProviderRefIndexField, err)
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx,
		&konveyoriov1alpha1.Agent{}, agentSkillCardRefIndexField,
		func(obj client.Object) []string {
			agent := obj.(*konveyoriov1alpha1.Agent)
			refs := make([]string, len(agent.Spec.SkillCards))
			for i, s := range agent.Spec.SkillCards {
				refs[i] = s.Ref
			}
			return refs
		},
	); err != nil {
		return fmt.Errorf("indexing %s: %w", agentSkillCardRefIndexField, err)
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx,
		&konveyoriov1alpha1.Agent{}, agentSkillCollectionRefIndexField,
		func(obj client.Object) []string {
			agent := obj.(*konveyoriov1alpha1.Agent)
			refs := make([]string, len(agent.Spec.SkillCollections))
			for i, s := range agent.Spec.SkillCollections {
				refs[i] = s.Ref
			}
			return refs
		},
	); err != nil {
		return fmt.Errorf("indexing %s: %w", agentSkillCollectionRefIndexField, err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&konveyoriov1alpha1.Agent{}).
		Watches(&konveyoriov1alpha1.LLMProvider{},
			handler.EnqueueRequestsFromMapFunc(r.findAgentsForResource(agentProviderRefIndexField))).
		Watches(&konveyoriov1alpha1.SkillCard{},
			handler.EnqueueRequestsFromMapFunc(r.findAgentsForResource(agentSkillCardRefIndexField))).
		Watches(&konveyoriov1alpha1.SkillCollection{},
			handler.EnqueueRequestsFromMapFunc(r.findAgentsForResource(agentSkillCollectionRefIndexField))).
		Named("agent").
		Complete(r)
}

// findAgentsForResource returns a mapFunc that uses the given field index
// to find Agents referencing the changed resource.
func (r *AgentReconciler) findAgentsForResource(indexField string) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		var agentList konveyoriov1alpha1.AgentList
		if err := r.List(ctx, &agentList,
			client.InNamespace(obj.GetNamespace()),
			client.MatchingFields{indexField: obj.GetName()},
		); err != nil {
			log.FromContext(ctx).Error(err, "Failed to list Agents for resource",
				"resource", obj.GetName(), "index", indexField)
			return nil
		}

		requests := make([]reconcile.Request, len(agentList.Items))
		for i, agent := range agentList.Items {
			requests[i] = reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: agent.Namespace,
					Name:      agent.Name,
				},
			}
		}
		return requests
	}
}
