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

	batchv1 "k8s.io/api/batch/v1"
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

	// EnumerationImage runs `skill-loader materialize` when a collection names
	// an image source. That binary ships only in the controller's own image,
	// so this is normally SKILL_LOADER_IMAGE. There is no default: an image
	// without it produces a pod that cannot exec.
	EnumerationImage string

	// EnumerationServiceAccount is the identity the enumeration Job runs as.
	// It writes SkillCards, so it needs more than the controller's own Job
	// permissions; see the trust boundary in skillcollection_enumerate.go.
	EnumerationServiceAccount string
}

// +kubebuilder:rbac:groups=konveyor.io,resources=skillcollections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=konveyor.io,resources=skillcollections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=konveyor.io,resources=skillcollections/finalizers,verbs=update
// +kubebuilder:rbac:groups=konveyor.io,resources=skillcards,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;delete
// The enumeration Job runs in the collection's namespace and needs an identity
// there. Kubernetes forbids granting what the granter lacks, so the Role this
// creates can only ever carry the SkillCard permissions above.
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch

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

	// An image source means "every skill in here". The controller enumerates
	// it with a Job and owns a SkillCard per skill it finds, so a user points
	// at a source once rather than writing a card each.
	if collection.Spec.Image != "" {
		return r.reconcileImageSource(ctx, &collection, original)
	}

	// A collection that no longer names an image keeps neither the cards the
	// enumeration wrote nor a status naming them. Nothing else prunes them --
	// the Job that would is only created on the image path -- so they would
	// otherwise stay referenceable for the collection's whole life, pointing at
	// an image it has stopped pointing at.
	if err := r.dropEnumeratedSkillCards(ctx, &collection); err != nil {
		logger.Error(err, "Failed to drop cards from a previous image source")
		return ctrl.Result{}, err
	}

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
			// Nothing to resolve, the same as for a SkillCard with a git
			// source: the skill loader clones it at pod start, and whether the
			// repository holds a usable skill is settled there. Reporting this
			// as unresolved would leave the collection permanently not-Ready
			// for a source the AgentRun controller already stages.
			readyCount++
		default:
			notReadyReasons = append(notReadyReasons,
				fmt.Sprintf("skill %q: no source configured", skillRef.Name))
		}
	}

	if totalSkills == 0 {
		meta.SetStatusCondition(&collection.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: collection.Generation,
			Reason:             "NoSkills",
			Message:            "Collection has no skills",
		})
	} else if readyCount == totalSkills {
		meta.SetStatusCondition(&collection.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: collection.Generation,
			Reason:             reasonAllSkillsResolved,
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

const (
	// skillCardRefIndexField is the field index key for looking up
	// SkillCollections by their skillCardRef values.
	skillCardRefIndexField = ".spec.skills.skillCardRef"
)

// SetupWithManager sets up the controller with the Manager.
func (r *SkillCollectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&konveyoriov1alpha1.SkillCollection{},
		skillCardRefIndexField,
		extractSkillCardRefs,
	); err != nil {
		return fmt.Errorf("setting up field index for %s: %w", skillCardRefIndexField, err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&konveyoriov1alpha1.SkillCollection{}).
		Watches(
			&konveyoriov1alpha1.SkillCard{},
			handler.EnqueueRequestsFromMapFunc(r.findCollectionsForSkillCard),
		).
		// Enumeration Jobs and the SkillCards they produce are both owned by
		// the collection, so completion brings the reconcile back without
		// polling.
		Owns(&batchv1.Job{}).
		Owns(&konveyoriov1alpha1.SkillCard{}).
		Named("skillcollection").
		Complete(r)
}

// extractSkillCardRefs returns the skillCardRef values from a
// SkillCollection for field indexing.
func extractSkillCardRefs(obj client.Object) []string {
	collection, ok := obj.(*konveyoriov1alpha1.SkillCollection)
	if !ok {
		return nil
	}
	var refs []string
	for _, skill := range collection.Spec.Skills {
		if skill.SkillCardRef != "" {
			refs = append(refs, skill.SkillCardRef)
		}
	}
	return refs
}

// findCollectionsForSkillCard returns reconcile requests for all SkillCollections
// that reference the given SkillCard, using the field index for O(1) lookup.
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
	if err := r.List(ctx, &collectionList,
		client.InNamespace(skillCard.Namespace),
		client.MatchingFields{skillCardRefIndexField: skillCard.Name},
	); err != nil {
		logger.Error(err, "Failed to list SkillCollections for SkillCard", "skillCard", skillCard.Name)
		return nil
	}

	requests := make([]reconcile.Request, len(collectionList.Items))
	for i, collection := range collectionList.Items {
		requests[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: collection.Namespace,
				Name:      collection.Name,
			},
		}
	}

	return requests
}
