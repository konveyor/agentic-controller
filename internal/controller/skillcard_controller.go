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

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

// SkillCardReconciler reconciles a SkillCard object.
type SkillCardReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=konveyor.io,resources=skillcards,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=konveyor.io,resources=skillcards/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=konveyor.io,resources=skillcards/finalizers,verbs=update

// Reconcile handles SkillCard reconciliation.
//
// For POC, only the image source type is handled: the controller sets
// status.resolvedImage to the spec image and marks the SkillCard Ready.
// Git source and inline content resolution are deferred to Phase 3.
func (r *SkillCardReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var skillCard konveyoriov1alpha1.SkillCard
	if err := r.Get(ctx, req.NamespacedName, &skillCard); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.V(1).Info("Reconciling SkillCard", "name", skillCard.Name)

	// Track the original status for comparison.
	original := skillCard.DeepCopy()

	// Set observed generation.
	skillCard.Status.ObservedGeneration = skillCard.Generation

	switch {
	case skillCard.Spec.Image != "":
		r.reconcileImage(&skillCard)
	case skillCard.Spec.Source != "":
		r.reconcileSource(&skillCard)
	case skillCard.Spec.Inline != "":
		r.reconcileInline(&skillCard)
	default:
		skillCard.Status.ResolvedImage = ""
		// Otherwise a card that loses its source keeps reporting how it used
		// to be delivered, while Ready correctly flips to False.
		skillCard.Status.DeliveryMode = ""
		meta.SetStatusCondition(&skillCard.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: skillCard.Generation,
			Reason:             "NoSourceConfigured",
			Message:            "No image, source, or inline content is set",
		})
	}

	// Patch status if changed.
	if err := r.Status().Patch(ctx, &skillCard, client.MergeFrom(original)); err != nil {
		logger.Error(err, "Failed to patch SkillCard status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// reconcileImage handles SkillCards with an OCI image source.
//
// The ref is accepted at face value. Proving it resolves would need a registry
// client, pull secrets and network egress in the reconciler, and would still
// not prove the image holds a usable skill — the frontmatter that decides that
// is only readable once the volume is mounted. So this is checked at pod init
// by the skill loader, where the bytes actually are, and a bad ref fails the
// pod there rather than being reported here.
func (r *SkillCardReconciler) reconcileImage(sc *konveyoriov1alpha1.SkillCard) {
	sc.Status.ResolvedImage = sc.Spec.Image
	sc.Status.DeliveryMode = "image"
	meta.SetStatusCondition(&sc.Status.Conditions, metav1.Condition{
		Type:               ConditionTypeReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: sc.Generation,
		Reason:             "ImageResolved",
		Message:            fmt.Sprintf("OCI image ref accepted: %s", sc.Spec.Image),
	})
}

// reconcileSource handles SkillCards with a git source URL.
//
// Nothing is built: the skill loader clones the repository at pod start, so
// there is no image to resolve and status.resolvedImage stays empty. As with
// an image ref, whether the repository actually holds a usable skill is
// settled at pod init.
func (r *SkillCardReconciler) reconcileSource(sc *konveyoriov1alpha1.SkillCard) {
	sc.Status.ResolvedImage = ""
	sc.Status.DeliveryMode = "source"
	meta.SetStatusCondition(&sc.Status.Conditions, metav1.Condition{
		Type:               ConditionTypeReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: sc.Generation,
		Reason:             "SourceAccepted",
		Message:            fmt.Sprintf("git source accepted, cloned at pod start: %s", sc.Spec.Source),
	})
}

// reconcileInline handles SkillCards with inline markdown content.
//
// This is the one source the controller can genuinely validate: the content is
// already in the CR, so no network is involved. Without frontmatter carrying a
// name and description a skill is invisible to the agent runtime, and an
// unvalidated inline card would otherwise resolve, mount and report Ready
// while contributing nothing.
//
// The delivery mechanism is a ConfigMap the AgentRun controller creates, so
// there is no image and status.resolvedImage stays empty.
func (r *SkillCardReconciler) reconcileInline(sc *konveyoriov1alpha1.SkillCard) {
	sc.Status.ResolvedImage = ""
	sc.Status.DeliveryMode = "inline"

	if err := validateInlineSkill(sc.Spec.Inline); err != nil {
		meta.SetStatusCondition(&sc.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: sc.Generation,
			Reason:             "InvalidSkillContent",
			Message:            err.Error(),
		})
		return
	}

	meta.SetStatusCondition(&sc.Status.Conditions, metav1.Condition{
		Type:               ConditionTypeReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: sc.Generation,
		Reason:             "InlineAccepted",
		Message:            "inline skill content is valid",
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *SkillCardReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&konveyoriov1alpha1.SkillCard{}).
		Named("skillcard").
		Complete(r)
}
