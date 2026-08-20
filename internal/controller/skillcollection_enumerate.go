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
	"slices"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

// Enumerating a SkillCollection's image source.
//
// The kubelet must pull the image to run the skill anyway, so a short-lived
// Job that mounts it as the agent pod does can read it, and the controller
// needs no registry client. The Job writes the SkillCards itself rather than
// reporting a list back; ADR 0015 has the argument, and the trust boundary
// that buys is marked in internal/skills/materialize.go.

const (
	// enumerationJobPrefix names the Job that lists an image's skills.
	enumerationJobPrefix = "skill-enumerate-"

	// enumerationSrcDir is where the Job mounts the source. It only has to
	// agree with the args below.
	enumerationSrcDir = "/src"

	// labelSkillCollection marks SkillCards this controller generated, so
	// pruning can find them without guessing from names. Defined in the api
	// module, which the enumeration Job writing the label imports too.
	labelSkillCollection = konveyoriov1alpha1.LabelSkillCollection

	// materializeSubcommand is both the container name and the harness
	// subcommand it runs.
	materializeSubcommand = "materialize"

	// defaultEnumerationServiceAccount is the identity the Job runs as. It is
	// scoped to SkillCards in this namespace and nothing else; the controller
	// creates it and its Role in skillcollection_rbac.go, in whichever
	// namespace the collection lives in.
	defaultEnumerationServiceAccount = "skill-enumerator"

	// reasonAllSkillsResolved marks a collection whose skills are all
	// accounted for, however they were resolved.
	reasonAllSkillsResolved = "AllSkillsResolved"

	// reasonEnumerationFailed covers anything that stops the Job producing an
	// answer: a bad source, a missing identity, a misconfigured image.
	reasonEnumerationFailed = "EnumerationFailed"

	// enumerationDeadline bounds one enumeration. Without it a Job whose pod
	// is never admitted -- a namespace enforcing restricted Pod Security, a
	// node whose runtime has no ImageVolume support, an unpullable reference --
	// carries neither Complete nor Failed, and the collection reads
	// "Enumerating" for as long as it exists.
	enumerationDeadline = int64(10 * 60)
)

// rbacRetryInterval is how long to wait before looking again at a namespace
// the controller may not write RBAC into. Nothing the controller watches
// changes when an operator creates the identity by hand, so this is the only
// thing that brings the collection back.
var rbacRetryInterval = 5 * time.Minute

// terminalError is an enumeration failure the next reconcile would only
// reproduce: a Job that already failed, an image the deployment never set.
// It belongs on the collection as a message rather than back in the queue.
// Anything not wrapped in one is retried, because most of what can go wrong
// here is an API call that can succeed next time.
type terminalError struct{ err error }

func (e terminalError) Error() string { return e.err.Error() }
func (e terminalError) Unwrap() error { return e.err }

func terminal(format string, args ...any) error {
	return terminalError{err: fmt.Errorf(format, args...)}
}

func isTerminal(err error) bool {
	var t terminalError
	return stderrors.As(err, &t)
}

// reconcileImageSource enumerates an image and materializes a SkillCard per
// skill it holds.
func (r *SkillCollectionReconciler) reconcileImageSource(
	ctx context.Context,
	collection *konveyoriov1alpha1.SkillCollection,
	original *konveyoriov1alpha1.SkillCollection,
) (ctrl.Result, error) {
	found, err := r.enumerateImage(ctx, collection, collection.Spec.Image)
	if err != nil {
		meta.SetStatusCondition(&collection.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: collection.Generation,
			Reason:             reasonEnumerationFailed,
			Message:            err.Error(),
		})
		if _, patchErr := r.patchCollectionStatus(ctx, collection, original); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		switch {
		case errors.IsForbidden(err):
			// The cluster withholds a permission. Retrying cannot fix it and
			// the message says what to create instead, but nothing this
			// controller watches changes when an operator does, so time it
			// rather than waiting for an event that will not come.
			return ctrl.Result{RequeueAfter: rbacRetryInterval}, nil
		case isTerminal(err):
			// The same answer every time until the spec changes, which is an
			// event we do get.
			return ctrl.Result{}, nil
		default:
			// Everything else is worth retrying, and reporting it without
			// returning it would wedge the collection: a condition identical
			// to the stored one patches to nothing and produces no event, and
			// with no Job and no cards yet neither watch can bring us back.
			return ctrl.Result{}, err
		}
	}
	if found == nil {
		// Job still running. The watch on Jobs brings us back.
		meta.SetStatusCondition(&collection.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: collection.Generation,
			Reason:             "Enumerating",
			Message:            fmt.Sprintf("Reading the skills in %s", collection.Spec.Image),
		})
		return r.patchCollectionStatus(ctx, collection, original)
	}

	collection.Status.ResolvedSkills = found
	meta.SetStatusCondition(&collection.Status.Conditions, metav1.Condition{
		Type:               ConditionTypeReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: collection.Generation,
		Reason:             reasonAllSkillsResolved,
		Message:            fmt.Sprintf("Enumerated %d skills from %s", len(found), collection.Spec.Image),
	})
	return r.patchCollectionStatus(ctx, collection, original)
}

// enumerationJobName is derived from the collection and its generation, so a
// spec change starts a fresh Job rather than reading a stale answer.
func enumerationJobName(collection *konveyoriov1alpha1.SkillCollection) string {
	return sanitizeVolumeName(fmt.Sprintf("%s%s-gen%d",
		enumerationJobPrefix, collection.Name, collection.Generation))
}

// enumerateImage drives the Job and returns the skills it found. A nil list
// with a nil error means the Job is still running and the caller should wait
// for the watch to bring it back.
func (r *SkillCollectionReconciler) enumerateImage(
	ctx context.Context,
	collection *konveyoriov1alpha1.SkillCollection,
	image string,
) ([]string, error) {
	name := enumerationJobName(collection)
	key := types.NamespacedName{Namespace: collection.Namespace, Name: name}

	var job batchv1.Job
	switch err := r.Get(ctx, key, &job); {
	case errors.IsNotFound(err):
		// A spec change starts a fresh Job under a new name, which leaves the
		// previous generation's behind for the collection's whole lifetime, and
		// a generation bumped mid-run would otherwise leave two pods
		// materializing against the same label and pruning each other's cards.
		//
		// Deleting is asynchronous even with foreground propagation, so the
		// next generation waits for the last one to go rather than starting
		// alongside it. The watch on Jobs brings us back when it does.
		stale, err := r.deleteStaleEnumerationJobs(ctx, collection, name)
		if err != nil {
			return nil, err
		}
		if stale {
			return nil, nil
		}
		// The Job runs in this collection's namespace, so its identity has to
		// exist there. Provision it: a cluster that refuses the controller RBAC
		// gets a message naming what to create, instead of a collection reading
		// Enumerating forever.
		if err := r.ensureEnumeratorRBAC(ctx, collection.Namespace); err != nil {
			return nil, err
		}
		if err := r.createEnumerationJob(ctx, collection, image, name); err != nil {
			return nil, err
		}
		return nil, nil
	case err != nil:
		return nil, err
	}

	// Conditions, not the raw counters, and the same helpers the Gateway's Job
	// uses: a Job whose pod cannot be created at all leaves both counters at
	// zero, which is indistinguishable from still running.
	if !isJobComplete(&job) {
		return nil, nil
	}
	if !isJobSucceeded(&job) {
		return nil, terminal("enumeration job %s failed; see its logs", name)
	}

	// The Job wrote the cards itself, so there is nothing to read back. What
	// exists is the answer.
	found, err := r.ownedSkillCards(ctx, collection)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		// A succeeded Job wrote at least one card: a source holding no skill
		// fails it. So an empty list is this cache not having seen the writes
		// yet -- they went to the API server from the Job's own client, on a
		// different watch stream to the one that delivered the Job's Complete
		// condition. Publishing it would answer "Enumerated 0 skills" and mark
		// the collection Ready.
		return nil, fmt.Errorf(
			"enumeration job %s succeeded but none of the SkillCards it wrote are visible yet", name)
	}
	return found, nil
}

// deleteStaleEnumerationJobs removes enumeration Jobs from prior generations,
// reporting whether any is still on its way out.
func (r *SkillCollectionReconciler) deleteStaleEnumerationJobs(
	ctx context.Context,
	collection *konveyoriov1alpha1.SkillCollection,
	current string,
) (bool, error) {
	var jobs batchv1.JobList
	if err := r.List(ctx, &jobs,
		client.InNamespace(collection.Namespace),
		client.MatchingLabels{labelSkillCollection: collection.Name}); err != nil {
		return false, fmt.Errorf("listing enumeration jobs: %w", err)
	}
	remaining := false
	for i := range jobs.Items {
		if jobs.Items[i].Name == current {
			continue
		}
		// Both prune SkillCards by collection label alone, with no notion of
		// which generation wrote them, so two overlapping materializations
		// delete each other's cards for skills unique to their own image. The
		// caller therefore waits on this rather than starting the next Job
		// alongside the pod being torn down: foreground propagation orders the
		// deletion, it does not block the client issuing it.
		remaining = true
		if err := r.Delete(ctx, &jobs.Items[i],
			client.PropagationPolicy(metav1.DeletePropagationForeground)); err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			return true, fmt.Errorf("deleting stale enumeration job %q: %w", jobs.Items[i].Name, err)
		}
	}
	return remaining, nil
}

// dropEnumeratedSkillCards removes what a previous image source left behind,
// for a collection that no longer has one.
func (r *SkillCollectionReconciler) dropEnumeratedSkillCards(
	ctx context.Context,
	collection *konveyoriov1alpha1.SkillCollection,
) error {
	if len(collection.Status.ResolvedSkills) == 0 {
		return nil
	}
	if _, err := r.deleteStaleEnumerationJobs(ctx, collection, ""); err != nil {
		return err
	}
	var owned konveyoriov1alpha1.SkillCardList
	if err := r.List(ctx, &owned,
		client.InNamespace(collection.Namespace),
		client.MatchingLabels{labelSkillCollection: collection.Name}); err != nil {
		return fmt.Errorf("listing generated SkillCards: %w", err)
	}
	for i := range owned.Items {
		if err := r.Delete(ctx, &owned.Items[i]); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("deleting generated SkillCard %q: %w", owned.Items[i].Name, err)
		}
	}
	collection.Status.ResolvedSkills = nil
	return nil
}

// ownedSkillCards lists what the enumeration Job created, which is the
// collection's resolved set.
func (r *SkillCollectionReconciler) ownedSkillCards(
	ctx context.Context,
	collection *konveyoriov1alpha1.SkillCollection,
) ([]string, error) {
	var owned konveyoriov1alpha1.SkillCardList
	if err := r.List(ctx, &owned,
		client.InNamespace(collection.Namespace),
		client.MatchingLabels{labelSkillCollection: collection.Name}); err != nil {
		return nil, fmt.Errorf("listing owned SkillCards: %w", err)
	}
	names := make([]string, 0, len(owned.Items))
	for i := range owned.Items {
		names = append(names, owned.Items[i].Name)
	}
	// Sorted so status.resolvedSkills does not churn with List order.
	slices.Sort(names)
	return names, nil
}

func (r *SkillCollectionReconciler) createEnumerationJob(
	ctx context.Context,
	collection *konveyoriov1alpha1.SkillCollection,
	image, name string,
) error {
	// The collection name is a label value on the Job, its pod and every card
	// the Job writes, and that is how the cards are found again. Object names
	// go to 253 characters and label values stop at 63, so say which limit was
	// hit here rather than letting the API server reject the Job with a
	// message about a field the user never wrote.
	if errs := validation.IsValidLabelValue(collection.Name); len(errs) > 0 {
		return terminal(
			"cannot enumerate: the collection name is used as a label value on the cards it owns, and %s",
			strings.Join(errs, "; "))
	}

	// The Job runs /skill-loader, which ships only in the controller's image,
	// so there is no other image this could sensibly default to. An empty one
	// is a deployment mistake rather than something to paper over: the pod
	// would be rejected for a missing image, or exec nothing if some other
	// image were substituted.
	runner := r.EnumerationImage
	if runner == "" {
		return terminal(
			"no enumeration image configured: set SKILL_LOADER_IMAGE (or ENUMERATION_IMAGE) " +
				"on the controller to an image carrying /skill-loader")
	}

	backoff := int32(0)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: collection.Namespace,
			Labels: map[string]string{
				labelManagedBy:       managedByLabel,
				labelSkillCollection: collection.Name,
			},
		},
		Spec: batchv1.JobSpec{
			// One shot. A source that cannot be read will not become readable
			// on retry, and the failure should surface rather than loop.
			BackoffLimit: &backoff,
			// A pod that is never admitted at all -- restricted Pod Security,
			// no ImageVolume support on the node, a reference that will not
			// pull -- leaves the Job with neither Complete nor Failed, and the
			// conditions below cannot tell that from still running. The
			// deadline is what turns it into an answer.
			ActiveDeadlineSeconds: ptr.To(enumerationDeadline),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{labelSkillCollection: collection.Name},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					// The Job writes SkillCards, so it needs an identity. See
					// the trust boundary above.
					ServiceAccountName: r.enumerationServiceAccount(),
					// Admissible under the restricted Pod Security Standards,
					// the same as the manager's own pod. This one mounts a
					// user-supplied image, so it has the least reason of any
					// pod here to be the one that needs an exemption.
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptr.To(true),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name:  materializeSubcommand,
						Image: runner,
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							ReadOnlyRootFilesystem:   ptr.To(true),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
						// Named rather than left to the image's ENTRYPOINT,
						// which an agent image may wrap in a script that
						// ignores its arguments.
						Command: []string{loaderBinary},
						Args: []string{
							materializeSubcommand, enumerationSrcDir,
						},
						Env: []corev1.EnvVar{
							{Name: "KONVEYOR_COLLECTION_NAME", Value: collection.Name},
							{Name: "KONVEYOR_COLLECTION_UID", Value: string(collection.UID)},
							{Name: "KONVEYOR_NAMESPACE", Value: collection.Namespace},
							// From the collection, never from the source.
							{Name: "KONVEYOR_SKILL_IMAGE", Value: image},
							{Name: "KONVEYOR_SKILL_TYPE", Value: string(collection.Spec.Type)},
						},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "src",
							MountPath: enumerationSrcDir,
							ReadOnly:  true,
						}},
					}},
					Volumes: []corev1.Volume{{
						Name: "src",
						VolumeSource: corev1.VolumeSource{
							Image: &corev1.ImageVolumeSource{Reference: image},
						},
					}},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(collection, job, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on enumeration job: %w", err)
	}
	if err := r.Create(ctx, job); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("creating enumeration job: %w", err)
	}
	return nil
}

// patchCollectionStatus writes status and returns, so each branch above reads
// as one line.
func (r *SkillCollectionReconciler) patchCollectionStatus(
	ctx context.Context,
	collection, original *konveyoriov1alpha1.SkillCollection,
) (ctrl.Result, error) {
	if err := r.Status().Patch(ctx, collection, client.MergeFrom(original)); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// enumerationServiceAccount is the identity the enumeration Job runs as.
func (r *SkillCollectionReconciler) enumerationServiceAccount() string {
	if r.EnumerationServiceAccount != "" {
		return r.EnumerationServiceAccount
	}
	return defaultEnumerationServiceAccount
}
