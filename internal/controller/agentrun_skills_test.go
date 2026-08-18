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
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

const (
	skillTestNS   = "skills-test"
	testAgentName = "agent"
	testSkillImg  = "quay.io/konveyor/skills:v0.1.0"
	testSubPath   = "plan"
	testSkillRepo = "https://example.com/acme.git"

	// The collection the multi-source tests reference.
	testCollName = "coll"

	// A minimal valid inline skill. Load policy is spec.type on the card, so
	// the content carries none.
	testInlineSkill = "---\nname: house-rules\ndescription: d\n---\n"

	// The skill testInlineSkill declares, which is also the SkillCard name the
	// tests give it.
	testInlineName = "house-rules"

	// testLoaderImage stands in for the controller's own image.
	testLoaderImage = "quay.io/konveyor/agentic-controller:v1"
)

func skillScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := konveyoriov1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	// createSandbox writes a Sandbox, so the fake client has to know the type.
	if err := sandboxv1beta1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func skillCard(name string, mutate func(*konveyoriov1alpha1.SkillCard)) *konveyoriov1alpha1.SkillCard {
	sc := &konveyoriov1alpha1.SkillCard{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: skillTestNS},
	}
	mutate(sc)
	return sc
}

func agentWithCards(refs ...string) *konveyoriov1alpha1.Agent {
	a := &konveyoriov1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: testAgentName, Namespace: skillTestNS},
		Spec:       konveyoriov1alpha1.AgentSpec{Image: "quay.io/konveyor/agent-java:latest"},
	}
	for _, r := range refs {
		a.Spec.SkillCards = append(a.Spec.SkillCards, konveyoriov1alpha1.AgentSkillCardRef{Ref: r})
	}
	return a
}

func agentRun(name string) *konveyoriov1alpha1.AgentRun {
	return &konveyoriov1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: skillTestNS},
		Spec:       konveyoriov1alpha1.AgentRunSpec{AgentRef: testAgentName},
	}
}

func newSkillReconciler(t *testing.T, objs ...runtime.Object) *AgentRunReconciler {
	t.Helper()
	s := skillScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objs...).Build()
	return &AgentRunReconciler{Client: c, Scheme: s}
}

func mountFor(mounts []corev1.VolumeMount, path string) *corev1.VolumeMount {
	for i := range mounts {
		if mounts[i].MountPath == path {
			return &mounts[i]
		}
	}
	return nil
}

// An image-sourced SkillCard is staged read-only, not mounted at its final
// path: one image may carry several skills, so the loader decides where each
// one lands.
func TestResolveSkillVolumesStagesImages(t *testing.T) {
	card := skillCard("konveyor", func(sc *konveyoriov1alpha1.SkillCard) {
		sc.Spec.Image = testSkillImg
		sc.Status.ResolvedImage = testSkillImg
	})
	r := newSkillReconciler(t, card)

	got, err := r.resolveSkillVolumes(context.Background(), agentRun("run"), agentWithCards("konveyor"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(got.volumes) != 1 || got.volumes[0].Image == nil {
		t.Fatalf("want one ImageVolume, got %+v", got.volumes)
	}
	if ref := got.volumes[0].Image.Reference; ref != testSkillImg {
		t.Errorf("image reference = %q", ref)
	}

	m := mountFor(got.mounts, skillsSrcDir+"/konveyor")
	if m == nil {
		t.Fatalf("want a staging mount at %s/konveyor, got %+v", skillsSrcDir, got.mounts)
	}
	if !m.ReadOnly {
		t.Error("staged sources must be read-only")
	}
	if len(got.inline) != 0 {
		t.Error("an image source should produce no inline content")
	}
	if len(got.declared) != 1 || got.declared[0].Git != nil {
		t.Errorf("want one non-git declared source, got %+v", got.declared)
	}
}

// Inline content is already bytes in the CR, so it becomes a ConfigMap. There
// is nothing to build and no registry involved.
func TestResolveSkillVolumesStagesInlineAsConfigMap(t *testing.T) {
	card := skillCard("house-rules", func(sc *konveyoriov1alpha1.SkillCard) {
		sc.Spec.Inline = testInlineSkill
	})
	r := newSkillReconciler(t, card)

	got, err := r.resolveSkillVolumes(context.Background(), agentRun("run"), agentWithCards("house-rules"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(got.volumes) != 1 || got.volumes[0].ConfigMap == nil {
		t.Fatalf("want a ConfigMap volume, got %+v", got.volumes)
	}
	want := inlineSkillConfigMapName("run", testInlineName)
	if name := got.volumes[0].ConfigMap.Name; name != want {
		t.Errorf("ConfigMap name = %q, want %q", name, want)
	}
	if got.inline["house-rules"] != card.Spec.Inline {
		t.Error("inline content was not carried through for materialization")
	}
	if mountFor(got.mounts, skillsSrcDir+"/house-rules") == nil {
		t.Errorf("want a staging mount, got %+v", got.mounts)
	}
}

// A git source produces no volume at all: the loader clones it, so the
// controller needs neither a builder nor a registry to deliver it.
func TestResolveSkillVolumesStagesGitWithoutAVolume(t *testing.T) {
	card := skillCard("acme", func(sc *konveyoriov1alpha1.SkillCard) {
		sc.Spec.Source = testSkillRepo
	})
	r := newSkillReconciler(t, card)

	got, err := r.resolveSkillVolumes(context.Background(), agentRun("run"), agentWithCards("acme"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(got.volumes) != 0 || len(got.mounts) != 0 {
		t.Errorf("a git source should need no volume, got %+v", got.volumes)
	}
	if len(got.declared) != 1 || got.declared[0].Git == nil || got.declared[0].Git.URL != testSkillRepo {
		t.Fatalf("declared sources = %+v", got.declared)
	}
}

// All three at once, which is the case the design has to stand behind.
func TestResolveSkillVolumesMixedSources(t *testing.T) {
	r := newSkillReconciler(t,
		skillCard("konveyor", func(sc *konveyoriov1alpha1.SkillCard) {
			sc.Spec.Image = testSkillImg
			sc.Status.ResolvedImage = testSkillImg
		}),
		skillCard("house-rules", func(sc *konveyoriov1alpha1.SkillCard) {
			sc.Spec.Inline = testInlineSkill
		}),
		skillCard("acme", func(sc *konveyoriov1alpha1.SkillCard) {
			sc.Spec.Source = testSkillRepo
		}),
	)

	got, err := r.resolveSkillVolumes(context.Background(), agentRun("run"), agentWithCards("konveyor", "house-rules", "acme"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(got.volumes) != 2 {
		t.Errorf("want 2 volumes (image + configmap), got %d", len(got.volumes))
	}
	gitCount := 0
	for _, d := range got.declared {
		if d.Git != nil {
			gitCount++
		}
	}
	if gitCount != 1 {
		t.Errorf("want 1 git source, got %d of %d declared", gitCount, len(got.declared))
	}
	// Every mounted source is staged, none at the final skills root.
	for _, m := range got.mounts {
		if m.MountPath == skillsDir {
			t.Errorf("a source was mounted at the assembled root %s", skillsDir)
		}
	}
}

func TestResolveSkillVolumesRejectsUnresolvedImage(t *testing.T) {
	card := skillCard("konveyor", func(sc *konveyoriov1alpha1.SkillCard) {
		sc.Spec.Image = testSkillImg // never reconciled
	})
	r := newSkillReconciler(t, card)

	if _, err := r.resolveSkillVolumes(context.Background(), agentRun("run"), agentWithCards("konveyor")); err == nil {
		t.Fatal("want an error when a SkillCard has no resolved image")
	}
}

// Reaching one skill twice, directly and through a collection, is an ordinary
// way to pin a card a collection also carries. It stages once.
func TestResolveSkillVolumesDedupesTheSameSource(t *testing.T) {
	card := skillCard("konveyor", func(sc *konveyoriov1alpha1.SkillCard) {
		sc.Spec.Image = testSkillImg
		sc.Status.ResolvedImage = testSkillImg
	})
	r := newSkillReconciler(t, card)

	got, err := r.resolveSkillVolumes(context.Background(), agentRun("run"), agentWithCards("konveyor", "konveyor"))
	if err != nil {
		t.Fatalf("resolving the same card twice: %v", err)
	}
	if len(got.declared) != 1 || len(got.volumes) != 1 || len(got.mounts) != 1 {
		t.Fatalf("want one source staged once, got %d declared, %d volumes, %d mounts",
			len(got.declared), len(got.volumes), len(got.mounts))
	}
}

// Two different sources under one name is a real conflict: only one of them
// can occupy the staging directory.
func TestResolveSkillVolumesRejectsConflictingSource(t *testing.T) {
	card := skillCard("konveyor", func(sc *konveyoriov1alpha1.SkillCard) {
		sc.Spec.Image = testSkillImg
		sc.Status.ResolvedImage = testSkillImg
	})
	collection := &konveyoriov1alpha1.SkillCollection{
		ObjectMeta: metav1.ObjectMeta{Name: testCollName, Namespace: skillTestNS},
		Spec: konveyoriov1alpha1.SkillCollectionSpec{
			Skills: []konveyoriov1alpha1.SkillCollectionSkillRef{
				{Name: "konveyor", Image: "quay.io/konveyor/other:v1"},
			},
		},
	}
	r := newSkillReconciler(t, card, collection)

	agent := agentWithCards("konveyor")
	agent.Spec.SkillCollections = []konveyoriov1alpha1.AgentSkillCollectionRef{{Ref: testCollName}}

	if _, err := r.resolveSkillVolumes(context.Background(), agentRun("run"), agent); err == nil {
		t.Fatal("want an error when two different sources claim one name")
	}
}

// A source name becomes a path segment on both sides of the boundary, in the
// staging mountPath here and in the loader's join over there.
func TestResolveSkillVolumesRejectsTraversalInASourceName(t *testing.T) {
	collection := &konveyoriov1alpha1.SkillCollection{
		ObjectMeta: metav1.ObjectMeta{Name: testCollName, Namespace: skillTestNS},
		Spec: konveyoriov1alpha1.SkillCollectionSpec{
			Skills: []konveyoriov1alpha1.SkillCollectionSkillRef{
				{Name: "../../etc/ssl/certs", Image: testSkillImg},
			},
		},
	}
	r := newSkillReconciler(t, collection)

	agent := agentWithCards()
	agent.Spec.SkillCollections = []konveyoriov1alpha1.AgentSkillCollectionRef{{Ref: testCollName}}

	if _, err := r.resolveSkillVolumes(context.Background(), agentRun("run"), agent); err == nil {
		t.Fatal("want an error when a source name escapes its staging directory")
	}
}

// An image collection has no spec.skills: the enumeration Job wrote a card per
// skill and status.resolvedSkills names them. Without this the whole
// enumeration path reaches no agent.
func TestResolveSkillVolumesStagesAnEnumeratedCollection(t *testing.T) {
	card := skillCard("coll-plan", func(sc *konveyoriov1alpha1.SkillCard) {
		sc.Spec.Image = testSkillImg
		sc.Spec.SubPath = testSubPath
		sc.Status.ResolvedImage = testSkillImg
	})
	collection := &konveyoriov1alpha1.SkillCollection{
		ObjectMeta: metav1.ObjectMeta{Name: testCollName, Namespace: skillTestNS},
		Spec:       konveyoriov1alpha1.SkillCollectionSpec{Image: testSkillImg},
		Status:     konveyoriov1alpha1.SkillCollectionStatus{ResolvedSkills: []string{"coll-plan"}},
	}
	r := newSkillReconciler(t, card, collection)

	agent := agentWithCards()
	agent.Spec.SkillCollections = []konveyoriov1alpha1.AgentSkillCollectionRef{{Ref: testCollName}}

	got, err := r.resolveSkillVolumes(context.Background(), agentRun("run"), agent)
	if err != nil {
		t.Fatalf("resolving an enumerated collection: %v", err)
	}
	if len(got.declared) != 1 || got.declared[0].Name != "coll-plan" || got.declared[0].SubPath != testSubPath {
		t.Fatalf("want the enumerated card staged, got %+v", got.declared)
	}
}

// The ConfigMap for an inline skill is owned by the AgentRun that mounts it,
// so two runs sharing one inline SkillCard must not share one ConfigMap.
// Sharing would let the second run rewrite the owner reference, and deleting
// that run would garbage collect the ConfigMap out from under the first.
func TestInlineSkillConfigMapIsScopedToTheRun(t *testing.T) {
	a := inlineSkillConfigMapName("run-a", "house-rules")
	b := inlineSkillConfigMapName("run-b", "house-rules")
	if a == b {
		t.Fatalf("two runs sharing a SkillCard got the same ConfigMap name %q", a)
	}

	card := skillCard("house-rules", func(sc *konveyoriov1alpha1.SkillCard) {
		sc.Spec.Inline = testInlineSkill
	})
	r := newSkillReconciler(t, card)

	got, err := r.resolveSkillVolumes(context.Background(), agentRun("run-a"), agentWithCards("house-rules"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if name := got.volumes[0].ConfigMap.Name; name != a {
		t.Errorf("volume references ConfigMap %q, want the run-scoped %q", name, a)
	}
}

// spec.type is an override that applies to whatever the source turns out to
// carry. The controller cannot see inside a bundle, so it forwards the policy
// rather than trying to name the skills it applies to.
func TestResolveSkillVolumesForwardsTypeOverride(t *testing.T) {
	card := skillCard("vendor-bundle", func(sc *konveyoriov1alpha1.SkillCard) {
		sc.Spec.Image = testSkillImg
		sc.Status.ResolvedImage = testSkillImg
		sc.Spec.Type = konveyoriov1alpha1.SkillCardTypeRule
	})
	r := newSkillReconciler(t, card)

	got, err := r.resolveSkillVolumes(context.Background(), agentRun("run"), agentWithCards("vendor-bundle"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got.declared) != 1 {
		t.Fatalf("declared = %+v", got.declared)
	}
	if got.declared[0].Type != string(konveyoriov1alpha1.SkillCardTypeRule) {
		t.Errorf("type = %q, want rule", got.declared[0].Type)
	}
}

// An unset spec.type must stay unset on the wire. If it arrived as "skill" the
// loader could not tell "the operator chose on-demand" from "the operator said
// nothing", and would silently demote a bundle whose frontmatter says rule.
func TestResolveSkillVolumesLeavesUnsetTypeEmpty(t *testing.T) {
	card := skillCard("konveyor", func(sc *konveyoriov1alpha1.SkillCard) {
		sc.Spec.Image = testSkillImg
		sc.Status.ResolvedImage = testSkillImg
	})
	r := newSkillReconciler(t, card)

	got, err := r.resolveSkillVolumes(context.Background(), agentRun("run"), agentWithCards("konveyor"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.declared[0].Type != "" {
		t.Errorf("type = %q, want empty so each skill decides", got.declared[0].Type)
	}
}

// subPath is how a card takes one skill out of an image holding several. The
// controller forwards it; only the loader can act on it, since only the loader
// sees inside the image.
func TestResolveSkillVolumesForwardsSubPath(t *testing.T) {
	card := skillCard("konveyor-"+testSubPath, func(sc *konveyoriov1alpha1.SkillCard) {
		sc.Spec.Image = testSkillImg
		sc.Status.ResolvedImage = testSkillImg
		sc.Spec.SubPath = testSubPath
		sc.Spec.Type = konveyoriov1alpha1.SkillCardTypeRule
	})
	r := newSkillReconciler(t, card)

	got, err := r.resolveSkillVolumes(context.Background(), agentRun("run"), agentWithCards("konveyor-"+testSubPath))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got.declared) != 1 {
		t.Fatalf("declared = %+v", got.declared)
	}
	if got.declared[0].SubPath != testSubPath {
		t.Errorf("subPath = %q, want plan", got.declared[0].SubPath)
	}
	if got.declared[0].Type != string(konveyoriov1alpha1.SkillCardTypeRule) {
		t.Errorf("type = %q, want rule", got.declared[0].Type)
	}
}

// Two cards into one image: the pull is shared, and each stages under its own
// name so the loader can assemble them separately.
func TestResolveSkillVolumesTwoCardsOneImage(t *testing.T) {
	r := newSkillReconciler(t,
		skillCard(testSubPath, func(sc *konveyoriov1alpha1.SkillCard) {
			sc.Spec.Image = testSkillImg
			sc.Status.ResolvedImage = testSkillImg
			sc.Spec.SubPath = testSubPath
		}),
		skillCard("javaee", func(sc *konveyoriov1alpha1.SkillCard) {
			sc.Spec.Image = testSkillImg
			sc.Status.ResolvedImage = testSkillImg
			sc.Spec.SubPath = "javaee-to-quarkus"
		}),
	)

	got, err := r.resolveSkillVolumes(context.Background(), agentRun("run"), agentWithCards(testSubPath, "javaee"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got.volumes) != 2 {
		t.Fatalf("want a volume per card, got %d", len(got.volumes))
	}
	for _, v := range got.volumes {
		if v.Image == nil || v.Image.Reference != testSkillImg {
			t.Errorf("both volumes should reference the same image, got %+v", v)
		}
	}
	subPaths := map[string]bool{}
	for _, d := range got.declared {
		subPaths[d.SubPath] = true
	}
	for _, want := range []string{testSubPath, "javaee-to-quarkus"} {
		if !subPaths[want] {
			t.Errorf("no declared source selects %q, got %+v", want, got.declared)
		}
	}
}

// A git card's ref travels in the git block; subPath is on the source, since
// it means the same thing for a clone and for a mount.
func TestResolveSkillVolumesForwardsGitRefAndSubPath(t *testing.T) {
	card := skillCard("acme", func(sc *konveyoriov1alpha1.SkillCard) {
		sc.Spec.Source = testSkillRepo
		sc.Spec.Ref = "v2.1.0"
		sc.Spec.SubPath = "skills/one"
	})
	r := newSkillReconciler(t, card)

	got, err := r.resolveSkillVolumes(context.Background(), agentRun("run"), agentWithCards("acme"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	d := got.declared[0]
	if d.Git == nil || d.Git.Ref != "v2.1.0" {
		t.Errorf("git = %+v, want ref v2.1.0", d.Git)
	}
	if d.SubPath != "skills/one" {
		t.Errorf("subPath = %q, want skills/one", d.SubPath)
	}
}

// The loader runs even with no skills, so there is one pod shape and one place
// that owns validation.
func TestSkillLoaderContainerAlwaysPresent(t *testing.T) {
	empty := &skillSources{inline: map[string]string{}}
	c := skillLoaderContainer(testLoaderImage, empty, nil)

	if c.Name != skillLoaderContainerName {
		t.Errorf("name = %q", c.Name)
	}
	// The binary is named rather than left to the image's ENTRYPOINT, and it is
	// an absolute path because the controller image is distroless.
	if len(c.Command) != 1 || c.Command[0] != loaderBinary {
		t.Errorf("command = %v, want the harness binary", c.Command)
	}
	if len(c.Args) < 1 || c.Args[0] != "load" {
		t.Errorf("args = %v", c.Args)
	}
	// Both directories explicit: their defaults come from the harness's own
	// environment, which this container does not inherit.
	args := strings.Join(c.Args, " ")
	for _, want := range []string{"--src-dir " + skillsSrcDir, "--dest-dir " + skillsDir} {
		if !strings.Contains(args, want) {
			t.Errorf("args = %v, want %q", c.Args, want)
		}
	}
	if len(c.Env) != 0 {
		t.Errorf("no declared sources should mean no env, got %v", c.Env)
	}
}

func TestSkillLoaderContainerDeclaresSources(t *testing.T) {
	sources := &skillSources{
		inline:   map[string]string{},
		declared: []skillSource{{Name: "acme", Git: &skillGitSource{URL: testSkillRepo}}},
	}
	c := skillLoaderContainer("img", sources, nil)

	var found *corev1.EnvVar
	for i := range c.Env {
		if c.Env[i].Name == skillSourcesEnv {
			found = &c.Env[i]
		}
	}
	if found == nil {
		t.Fatalf("want %s to be set, got %v", skillSourcesEnv, c.Env)
	}
	var decoded []skillSource
	if err := json.Unmarshal([]byte(found.Value), &decoded); err != nil {
		t.Fatalf("%s is not valid JSON: %v", skillSourcesEnv, err)
	}
	if len(decoded) != 1 || decoded[0].Git == nil || decoded[0].Git.URL != testSkillRepo {
		t.Errorf("decoded = %+v", decoded)
	}
}

// The ConfigMap carries the card's content under the one key an inline skill
// can have, and belongs to the run so it is collected with it.
func TestCreateInlineSkillConfigMapsWritesOwnedContent(t *testing.T) {
	run := agentRun("run")
	r := newSkillReconciler(t)
	ctx := context.Background()

	if err := r.createInlineSkillConfigMaps(ctx, run,
		map[string]string{testInlineName: testInlineSkill}); err != nil {
		t.Fatalf("create: %v", err)
	}

	var cm corev1.ConfigMap
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: skillTestNS, Name: inlineSkillConfigMapName("run", testInlineName),
	}, &cm); err != nil {
		t.Fatalf("ConfigMap not created: %v", err)
	}
	// A ConfigMap key cannot hold a path separator, so an inline skill is
	// exactly one SKILL.md.
	if got := cm.Data["SKILL.md"]; got != testInlineSkill {
		t.Errorf("SKILL.md = %q, want the card's content", got)
	}
	if len(cm.Data) != 1 {
		t.Errorf("want a single key, got %v", cm.Data)
	}
	if len(cm.OwnerReferences) == 0 || cm.OwnerReferences[0].Name != "run" {
		t.Errorf("not owned by the run, so it would outlive it: %+v", cm.OwnerReferences)
	}
}

// An AgentRun reconciles more than once, so creating the ConfigMap has to
// converge rather than fail on the second pass.
func TestCreateInlineSkillConfigMapsIsIdempotent(t *testing.T) {
	run := agentRun("run")
	r := newSkillReconciler(t)
	ctx := context.Background()
	inline := map[string]string{testInlineName: testInlineSkill}

	if err := r.createInlineSkillConfigMaps(ctx, run, inline); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if err := r.createInlineSkillConfigMaps(ctx, run, inline); err != nil {
		t.Fatalf("second pass should converge, not fail: %v", err)
	}
}

// Editing a SkillCard's inline content has to reach the pod. If the existing
// ConfigMap were left alone, the run would keep mounting the old rule while
// the card reported the new one.
func TestCreateInlineSkillConfigMapsUpdatesChangedContent(t *testing.T) {
	run := agentRun("run")
	r := newSkillReconciler(t)
	ctx := context.Background()

	if err := r.createInlineSkillConfigMaps(ctx, run,
		map[string]string{testInlineName: testInlineSkill}); err != nil {
		t.Fatalf("first pass: %v", err)
	}

	const edited = "---\nname: house-rules\ndescription: d\n---\n\nNo javax, ever.\n"
	if err := r.createInlineSkillConfigMaps(ctx, run,
		map[string]string{testInlineName: edited}); err != nil {
		t.Fatalf("second pass: %v", err)
	}

	var cm corev1.ConfigMap
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: skillTestNS, Name: inlineSkillConfigMapName("run", testInlineName),
	}, &cm); err != nil {
		t.Fatal(err)
	}
	if cm.Data["SKILL.md"] != edited {
		t.Errorf("SKILL.md = %q, want the edited content", cm.Data["SKILL.md"])
	}
}

// The loader runs the controller's image, never the agent's. Using the agent's
// would make "carries our harness binary" a requirement of every agent image,
// and the failure when one does not is an init container that cannot start,
// with no log to say why.
func TestSkillLoaderUsesTheControllerImageNotTheAgents(t *testing.T) {
	card := skillCard(testInlineName, func(sc *konveyoriov1alpha1.SkillCard) {
		sc.Spec.Inline = testInlineSkill
	})
	agent := agentWithCards(testInlineName)
	agent.Spec.Image = "quay.io/example/someones-own-agent:v1"

	run := agentRun("run")
	r := newSkillReconciler(t, card, run, agent)
	r.SkillLoaderImage = testLoaderImage
	ctx := context.Background()

	if _, err := r.createSandbox(ctx, run, agent); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	var sandbox sandboxv1beta1.Sandbox
	if err := r.Get(ctx, client.ObjectKey{Namespace: skillTestNS, Name: run.Name}, &sandbox); err != nil {
		t.Fatalf("sandbox not created: %v", err)
	}

	var loader *corev1.Container
	for i := range sandbox.Spec.PodTemplate.Spec.InitContainers {
		if sandbox.Spec.PodTemplate.Spec.InitContainers[i].Name == skillLoaderContainerName {
			loader = &sandbox.Spec.PodTemplate.Spec.InitContainers[i]
		}
	}
	if loader == nil {
		t.Fatal("no skill-loader init container")
	}
	if loader.Image != testLoaderImage {
		t.Errorf("loader image = %q, want the controller image %q", loader.Image, testLoaderImage)
	}
	if loader.Image == agent.Spec.Image {
		t.Error("loader is running the agent's image")
	}
	// The agent container still runs the agent's image.
	if got := sandbox.Spec.PodTemplate.Spec.Containers[0].Image; got != agent.Spec.Image {
		t.Errorf("agent container image = %q, want %q", got, agent.Spec.Image)
	}
}
