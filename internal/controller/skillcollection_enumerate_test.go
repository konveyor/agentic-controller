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
	"errors"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

const (
	enumTestImage = "quay.io/konveyor/skills:v1"
	skillVerify   = "verify"
)

func collection(name string, mutate func(*konveyoriov1alpha1.SkillCollection)) *konveyoriov1alpha1.SkillCollection {
	c := &konveyoriov1alpha1.SkillCollection{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: skillTestNS, Generation: 1, UID: "collection-uid",
		},
		Spec: konveyoriov1alpha1.SkillCollectionSpec{Image: enumTestImage},
	}
	if mutate != nil {
		mutate(c)
	}
	return c
}

func newCollectionReconciler(t *testing.T, objs ...runtime.Object) *SkillCollectionReconciler {
	t.Helper()
	s := skillScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithRuntimeObjects(objs...).
		// Status().Patch needs the subresource registered, as on a real server.
		WithStatusSubresource(
			&konveyoriov1alpha1.SkillCollection{},
			&konveyoriov1alpha1.SkillCard{},
		).
		Build()
	return &SkillCollectionReconciler{
		Client:           c,
		Scheme:           s,
		EnumerationImage: "quay.io/konveyor/agent-base:latest",
	}
}

// finishedJob is an enumeration Job the Job controller has settled, carrying
// the condition the reconciler reads. The raw Succeeded and Failed counters
// are set too, but they are not what decides: a Job whose pod cannot be
// created leaves both at zero forever.
func finishedJob(col *konveyoriov1alpha1.SkillCollection, succeeded bool) *batchv1.Job {
	condType, status := batchv1.JobFailed, batchv1.JobStatus{Failed: 1}
	if succeeded {
		condType, status = batchv1.JobComplete, batchv1.JobStatus{Succeeded: 1}
	}
	status.Conditions = []batchv1.JobCondition{{Type: condType, Status: corev1.ConditionTrue}}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      enumerationJobName(col),
			Namespace: skillTestNS,
			Labels:    map[string]string{labelSkillCollection: col.Name},
		},
		Status: status,
	}
}

// generatedCard is what the enumeration Job would have written.
func generatedCard(collectionName, skill, subPath string) *konveyoriov1alpha1.SkillCard {
	return &konveyoriov1alpha1.SkillCard{
		ObjectMeta: metav1.ObjectMeta{
			Name:      collectionName + "-" + skill,
			Namespace: skillTestNS,
			Labels:    map[string]string{labelSkillCollection: collectionName},
		},
		Spec: konveyoriov1alpha1.SkillCardSpec{Image: enumTestImage, SubPath: subPath},
	}
}

// The job name carries the generation, so editing a collection starts a fresh
// enumeration rather than trusting the previous run.
func TestEnumerationJobNameIsGenerationScoped(t *testing.T) {
	gen1 := enumerationJobName(collection("konveyor", nil))
	gen2 := enumerationJobName(collection("konveyor", func(c *konveyoriov1alpha1.SkillCollection) {
		c.Generation = 2
	}))
	if gen1 == gen2 {
		t.Fatalf("both generations produced %q; a spec change would reuse a stale job", gen1)
	}
}

// First reconcile creates the job and reports nothing yet.
func TestEnumerateImageCreatesJobThenWaits(t *testing.T) {
	col := collection("konveyor", nil)
	r := newCollectionReconciler(t, col)

	found, err := r.enumerateImage(context.Background(), col, enumTestImage)
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	if found != nil {
		t.Errorf("want nil while the job runs, got %+v", found)
	}

	var job batchv1.Job
	if err := r.Get(context.Background(), client.ObjectKey{
		Namespace: skillTestNS, Name: enumerationJobName(col),
	}, &job); err != nil {
		t.Fatalf("job was not created: %v", err)
	}

	spec := job.Spec.Template.Spec
	if len(spec.Volumes) != 1 || spec.Volumes[0].Image == nil {
		t.Fatalf("want the source mounted as an ImageVolume, got %+v", spec.Volumes)
	}
	if ref := spec.Volumes[0].Image.Reference; ref != enumTestImage {
		t.Errorf("image reference = %q", ref)
	}
	if args := strings.Join(spec.Containers[0].Args, " "); !strings.Contains(args, materializeSubcommand) {
		t.Errorf("args = %q, want a materialize", args)
	}
	// The job writes SkillCards, so it needs an identity of its own.
	if spec.ServiceAccountName == "" {
		t.Error("no ServiceAccount; the job could not write cards")
	}
	// One shot: a source that cannot be read will not become readable on retry.
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 0 {
		t.Errorf("backoffLimit = %v, want 0", job.Spec.BackoffLimit)
	}
	if len(job.OwnerReferences) == 0 || job.OwnerReferences[0].Name != col.Name {
		t.Errorf("job is not owned by the collection: %+v", job.OwnerReferences)
	}
}

// The job needs to know which collection owns what it writes, and which image
// to point the cards at.
func TestEnumerationJobCarriesOwnerAndImage(t *testing.T) {
	col := collection("konveyor", func(c *konveyoriov1alpha1.SkillCollection) {
		c.Spec.Type = konveyoriov1alpha1.SkillCardTypeRule
	})
	r := newCollectionReconciler(t, col)

	if _, err := r.enumerateImage(context.Background(), col, enumTestImage); err != nil {
		t.Fatalf("enumerate: %v", err)
	}

	var job batchv1.Job
	if err := r.Get(context.Background(), client.ObjectKey{
		Namespace: skillTestNS, Name: enumerationJobName(col),
	}, &job); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{}
	for _, e := range job.Spec.Template.Spec.Containers[0].Env {
		env[e.Name] = e.Value
	}
	want := map[string]string{
		"KONVEYOR_COLLECTION_NAME": col.Name,
		"KONVEYOR_COLLECTION_UID":  string(col.UID),
		"KONVEYOR_NAMESPACE":       skillTestNS,
		// From the collection, so a skill cannot nominate its own image.
		"KONVEYOR_SKILL_IMAGE": enumTestImage,
		"KONVEYOR_SKILL_TYPE":  string(konveyoriov1alpha1.SkillCardTypeRule),
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
}

func TestEnumerateImageReportsFailedJob(t *testing.T) {
	col := collection("konveyor", nil)
	job := finishedJob(col, false)
	r := newCollectionReconciler(t, col, job)

	if _, err := r.enumerateImage(context.Background(), col, enumTestImage); err == nil {
		t.Fatal("want an error when the enumeration job failed")
	}
}

// Once the job succeeds, the cards it wrote are the answer. Nothing is
// serialized back, so what exists is what was found.
func TestEnumerateImageReturnsTheCardsTheJobWrote(t *testing.T) {
	col := collection("konveyor", nil)
	job := finishedJob(col, true)
	r := newCollectionReconciler(t, col, job,
		generatedCard("konveyor", testSubPath, testSubPath),
		generatedCard("konveyor", skillVerify, skillVerify))

	found, err := r.enumerateImage(context.Background(), col, enumTestImage)
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("found = %v", found)
	}
	if found[0] != "konveyor-"+testSubPath {
		t.Errorf("names are not sorted or scoped: %v", found)
	}
}

// Another collection's cards are not counted as this one's.
func TestEnumerateImageIgnoresOtherCollectionsCards(t *testing.T) {
	col := collection("mine", nil)
	job := finishedJob(col, true)
	r := newCollectionReconciler(t, col, job,
		generatedCard("mine", testSubPath, testSubPath),
		generatedCard("theirs", skillVerify, skillVerify))

	found, err := r.enumerateImage(context.Background(), col, enumTestImage)
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	if len(found) != 1 || found[0] != "mine-"+testSubPath {
		t.Fatalf("found = %v, want only this collection's card", found)
	}
}

// The whole flow: create the job, then report what it produced.
func TestReconcileImageSourceEndToEnd(t *testing.T) {
	col := collection("konveyor", nil)
	r := newCollectionReconciler(t, col)
	ctx := context.Background()

	if _, err := r.reconcileImageSource(ctx, col, col.DeepCopy()); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if got := conditionReason(col); got != "Enumerating" {
		t.Fatalf("reason = %q, want Enumerating", got)
	}

	// The job runs, writes its cards, and finishes.
	name := enumerationJobName(col)
	var job batchv1.Job
	if err := r.Get(ctx, client.ObjectKey{Namespace: skillTestNS, Name: name}, &job); err != nil {
		t.Fatalf("job from the first pass: %v", err)
	}
	job.Status = finishedJob(col, true).Status
	if err := r.Status().Update(ctx, &job); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*konveyoriov1alpha1.SkillCard{
		generatedCard("konveyor", testSubPath, testSubPath),
		generatedCard("konveyor", skillVerify, skillVerify),
	} {
		if err := r.Create(ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := r.reconcileImageSource(ctx, col, col.DeepCopy()); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if got := conditionReason(col); got != reasonAllSkillsResolved {
		t.Fatalf("reason = %q, want %s", got, reasonAllSkillsResolved)
	}
	if len(col.Status.ResolvedSkills) != 2 {
		t.Errorf("resolvedSkills = %v", col.Status.ResolvedSkills)
	}
}

// A source the job could not read leaves the collection not-Ready with the
// reason attached, rather than silently resolving to nothing.
func TestReconcileImageSourceReportsEnumerationFailure(t *testing.T) {
	col := collection("konveyor", nil)
	job := finishedJob(col, false)
	r := newCollectionReconciler(t, col, job)

	if _, err := r.reconcileImageSource(context.Background(), col, col.DeepCopy()); err != nil {
		t.Fatalf("a failed job should be reported in status, not returned: %v", err)
	}
	if got := conditionReason(col); got != reasonEnumerationFailed {
		t.Fatalf("reason = %q, want EnumerationFailed", got)
	}
}

func conditionReason(c *konveyoriov1alpha1.SkillCollection) string {
	for _, cond := range c.Status.Conditions {
		if cond.Type == ConditionTypeReady {
			return cond.Reason
		}
	}
	return ""
}

// The Job runs /skill-loader, which ships only in the controller's image.
// There is no other image to fall back to, so an unconfigured one has to say
// so on the collection rather than schedule a pod that cannot exec.
func TestEnumerateImageWithoutARunnerImageFails(t *testing.T) {
	col := collection("konveyor", nil)
	r := newCollectionReconciler(t, col)
	r.EnumerationImage = ""

	_, err := r.reconcileImageSource(context.Background(), col, col.DeepCopy())
	if err != nil {
		t.Fatalf("a misconfiguration belongs in status, not returned: %v", err)
	}
	if got := conditionReason(col); got != reasonEnumerationFailed {
		t.Fatalf("reason = %q, want EnumerationFailed", got)
	}
	msg := conditionMessage(col)
	if !strings.Contains(msg, "SKILL_LOADER_IMAGE") {
		t.Errorf("message should name the env var to set, got: %s", msg)
	}
	// And no Job, since there is nothing runnable to put in it.
	var jobs batchv1.JobList
	if err := r.List(context.Background(), &jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 0 {
		t.Errorf("scheduled %d job(s) with no runner image", len(jobs.Items))
	}
}

func conditionMessage(c *konveyoriov1alpha1.SkillCollection) string {
	for _, cond := range c.Status.Conditions {
		if cond.Type == ConditionTypeReady {
			return cond.Message
		}
	}
	return ""
}

// The Job runs in the collection's namespace, so its identity has to exist
// there. Shipping one in the operator's namespace only ever served collections
// that happened to live there.
func TestEnumerationProvisionsItsIdentityInTheCollectionsNamespace(t *testing.T) {
	col := collection("konveyor", nil)
	r := newCollectionReconciler(t, col)
	ctx := context.Background()

	if _, err := r.enumerateImage(ctx, col, enumTestImage); err != nil {
		t.Fatalf("enumerate: %v", err)
	}

	var sa corev1.ServiceAccount
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: skillTestNS, Name: r.enumerationServiceAccount(),
	}, &sa); err != nil {
		t.Fatalf("no ServiceAccount in the collection's namespace: %v", err)
	}
	var role rbacv1.Role
	if err := r.Get(ctx, client.ObjectKey{Namespace: skillTestNS, Name: enumeratorRoleName}, &role); err != nil {
		t.Fatalf("no Role: %v", err)
	}
	// Only SkillCards. The controller cannot grant more than it holds, and the
	// blast radius of a pod that mounted a user's image should stay here.
	if len(role.Rules) != 1 || len(role.Rules[0].Resources) != 1 ||
		role.Rules[0].Resources[0] != "skillcards" {
		t.Errorf("role grants more than skillcards: %+v", role.Rules)
	}
	var binding rbacv1.RoleBinding
	if err := r.Get(ctx, client.ObjectKey{Namespace: skillTestNS, Name: enumeratorRoleName}, &binding); err != nil {
		t.Fatalf("no RoleBinding: %v", err)
	}
	if len(binding.Subjects) != 1 || binding.Subjects[0].Namespace != skillTestNS {
		t.Errorf("binding does not name this namespace's account: %+v", binding.Subjects)
	}
	// Not owned by the collection: several collections here share one identity,
	// and collecting it with whichever was deleted first breaks the others.
	if len(sa.OwnerReferences) != 0 {
		t.Errorf("ServiceAccount is owned by %+v, so it dies with one collection", sa.OwnerReferences)
	}
}

// A cluster may refuse the controller permission to write RBAC. That is a
// legitimate way to run it, and it has to read as an instruction rather than a
// collection stuck on Enumerating.
func TestEnumerationReportsWhenItMayNotCreateRBAC(t *testing.T) {
	col := collection("konveyor", nil)
	r := newCollectionReconciler(t, col)
	r.Client = forbidWrites{Client: r.Client, on: kindServiceAccount}

	_, err := r.reconcileImageSource(context.Background(), col, col.DeepCopy())
	if err != nil {
		t.Fatalf("a permission problem belongs in status, not returned: %v", err)
	}
	if got := conditionReason(col); got != reasonEnumerationFailed {
		t.Fatalf("reason = %q, want EnumerationFailed", got)
	}
	msg := conditionMessage(col)
	// The objects by name, not a manifest to apply: kustomize renders the ones
	// in config/rbac with a name prefix and the operator's own namespace, so an
	// operator told to apply them would create something the Job never names.
	for _, want := range []string{defaultEnumerationServiceAccount, enumeratorRoleName, skillTestNS} {
		if !strings.Contains(msg, want) {
			t.Errorf("message should name %q so it can be acted on, got: %s", want, msg)
		}
	}
}

// forbidWrites refuses to create one kind, the way a cluster that withholds
// RBAC from the controller would.
type forbidWrites struct {
	client.Client
	on string
}

func (f forbidWrites) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if _, ok := obj.(*corev1.ServiceAccount); ok && f.on == kindServiceAccount {
		return apierrors.NewForbidden(
			schema.GroupResource{Resource: "serviceaccounts"}, obj.GetName(),
			errors.New("not permitted"))
	}
	return f.Client.Create(ctx, obj, opts...)
}
