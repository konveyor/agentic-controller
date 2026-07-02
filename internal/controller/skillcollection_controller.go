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

// SkillCollectionReconciler reconciles a SkillCollection object.
type SkillCollectionReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=konveyor.io,resources=skillcollections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=konveyor.io,resources=skillcollections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=konveyor.io,resources=skillcollections/finalizers,verbs=update

// Reconcile handles SkillCollection reconciliation.
//
// The controller checks all referenced skills in the collection:
//   - skillCardRef: looks up the named SkillCard and checks its Ready condition
//   - image: the skill is inherently resolved (OCI image ref is self-contained)
//   - source: not yet supported (Phase 3)
//
// The collection is Ready when all skills are resolved.
func (r *SkillCollectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var collection konveyoriov1alpha1.SkillCollection
	if err := r.Get(ctx, req.NamespacedName, &collection); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.V(1).Info("Reconciling SkillCollection", "name", collection.Name)

	original := collection.DeepCopy()
	collection.Status.ObservedGeneration = collection.Generation

	totalSkills := len(collection.Spec.Skills)
	readyCount := 0
	var notReadyReasons []string

	for _, skillRef := range collection.Spec.Skills {
		switch {
		case skillRef.SkillCardRef != "":
			ready, reason := r.checkSkillCardRef(ctx, collection.Namespace, skillRef)
			if ready {
				readyCount++
			} else {
				notReadyReasons = append(notReadyReasons, reason)
			}
		case skillRef.Image != "":
			// An image ref in a collection is self-contained — no SkillCard
			// CR needed to resolve it. The image will be mounted directly
			// as an ImageVolume by the AgentRun controller.
			readyCount++
		case skillRef.Source != "":
			notReadyReasons = append(notReadyReasons,
				fmt.Sprintf("skill %q: git source resolution not yet implemented (Phase 3)", skillRef.Name))
		}
	}

	if readyCount == totalSkills {
		meta.SetStatusCondition(&collection.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: collection.Generation,
			Reason:             "AllSkillsResolved",
			Message:            fmt.Sprintf("All %d skills resolved", totalSkills),
		})
	} else {
		message := fmt.Sprintf("%d of %d skills resolved", readyCount, totalSkills)
		if len(notReadyReasons) > 0 {
			message = fmt.Sprintf("%s: %s", message, strings.Join(notReadyReasons, "; "))
		}
		meta.SetStatusCondition(&collection.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: collection.Generation,
			Reason:             "SkillsNotReady",
			Message:            message,
		})
	}

	if err := r.Status().Patch(ctx, &collection, client.MergeFrom(original)); err != nil {
		logger.Error(err, "Failed to patch SkillCollection status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// checkSkillCardRef looks up a referenced SkillCard and checks whether it is Ready.
func (r *SkillCollectionReconciler) checkSkillCardRef(
	ctx context.Context,
	namespace string,
	ref konveyoriov1alpha1.SkillCollectionSkillRef,
) (bool, string) {
	var sc konveyoriov1alpha1.SkillCard
	key := types.NamespacedName{Namespace: namespace, Name: ref.SkillCardRef}
	if err := r.Get(ctx, key, &sc); err != nil {
		if errors.IsNotFound(err) {
			return false, fmt.Sprintf("skill %q: SkillCard %q not found", ref.Name, ref.SkillCardRef)
		}
		return false, fmt.Sprintf("skill %q: error fetching SkillCard %q: %v", ref.Name, ref.SkillCardRef, err)
	}

	readyCond := meta.FindStatusCondition(sc.Status.Conditions, ConditionTypeReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionTrue {
		return false, fmt.Sprintf("skill %q: SkillCard %q is not Ready", ref.Name, ref.SkillCardRef)
	}

	return true, ""
}

// SetupWithManager sets up the controller with the Manager.
func (r *SkillCollectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&konveyoriov1alpha1.SkillCollection{}).
		Watches(
			&konveyoriov1alpha1.SkillCard{},
			handler.EnqueueRequestsFromMapFunc(r.findCollectionsForSkillCard),
		).
		Named("skillcollection").
		Complete(r)
}

// findCollectionsForSkillCard returns reconcile requests for all SkillCollections
// that reference the given SkillCard.
func (r *SkillCollectionReconciler) findCollectionsForSkillCard(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	logger := log.FromContext(ctx)
	skillCard, ok := obj.(*konveyoriov1alpha1.SkillCard)
	if !ok {
		return nil
	}

	var collectionList konveyoriov1alpha1.SkillCollectionList
	if err := r.List(ctx, &collectionList, client.InNamespace(skillCard.Namespace)); err != nil {
		logger.Error(err, "Failed to list SkillCollections")
		return nil
	}

	var requests []reconcile.Request
	for i := range collectionList.Items {
		collection := &collectionList.Items[i]
		for _, ref := range collection.Spec.Skills {
			if ref.SkillCardRef == skillCard.Name {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: collection.Namespace,
						Name:      collection.Name,
					},
				})
				break
			}
		}
	}

	return requests
}
