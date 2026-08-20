package skills

import (
	"context"
	"path/filepath"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

const testCollectionImage = "quay.io/konveyor/skills:v1"

func testOwner() Owner {
	return Owner{Name: srcKonveyor, UID: "collection-uid", Namespace: testNamespace}
}

// withClient swaps the in-cluster client for a fake one, since these tests are
// about what gets written rather than how the connection is made.
func withClient(t *testing.T, objs ...runtime.Object) client.Client {
	t.Helper()
	s := runtime.NewScheme()
	if err := konveyoriov1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objs...).Build()

	original := newClusterClient
	newClusterClient = func() (client.Client, error) { return c, nil }
	t.Cleanup(func() { newClusterClient = original })
	return c
}

func existingCard(name, subPath string) *konveyoriov1alpha1.SkillCard {
	return &konveyoriov1alpha1.SkillCard{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels:    map[string]string{labelSkillCollection: srcKonveyor},
		},
		Spec: konveyoriov1alpha1.SkillCardSpec{Image: testCollectionImage, SubPath: subPath},
	}
}

func TestMaterializeWritesACardPerSkill(t *testing.T) {
	src := t.TempDir()
	writeSkill(t, filepath.Join(src, skillPlan), skillPlan, nil)
	writeSkill(t, filepath.Join(src, "verify"), "verify", nil)
	c := withClient(t)

	names, err := Materialize(context.Background(), src, MaterializeOptions{
		Owner: testOwner(),
		Image: testCollectionImage,
		Type:  TypeRule,
	})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("names = %v", names)
	}

	var card konveyoriov1alpha1.SkillCard
	if err := c.Get(context.Background(), client.ObjectKey{
		Namespace: testNamespace, Name: "konveyor-plan",
	}, &card); err != nil {
		t.Fatalf("card not written: %v", err)
	}
	if card.Spec.SubPath != skillPlan {
		t.Errorf("subPath = %q", card.Spec.SubPath)
	}
	// The image comes from the collection, never from the walk: a skill must
	// not be able to nominate the image its card points at.
	if card.Spec.Image != testCollectionImage {
		t.Errorf("image = %q, want the collection's", card.Spec.Image)
	}
	if card.Spec.Type != konveyoriov1alpha1.SkillCardType(TypeRule) {
		t.Errorf("type = %q, want the collection's policy", card.Spec.Type)
	}
	if len(card.OwnerReferences) == 0 || card.OwnerReferences[0].Name != srcKonveyor {
		t.Errorf("card is not owned by the collection: %+v", card.OwnerReferences)
	}
	if card.Labels[labelSkillCollection] != srcKonveyor {
		t.Errorf("card is not labelled for pruning: %+v", card.Labels)
	}
}

// Nothing in the source can change which image the cards point at, even if a
// skill is named to look like one.
func TestMaterializeIgnoresTheSourceForImage(t *testing.T) {
	src := t.TempDir()
	writeSkill(t, filepath.Join(src, skillPlan), skillPlan, nil)
	c := withClient(t)

	if _, err := Materialize(context.Background(), src, MaterializeOptions{
		Owner: testOwner(),
		Image: testCollectionImage,
	}); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	var cards konveyoriov1alpha1.SkillCardList
	if err := c.List(context.Background(), &cards); err != nil {
		t.Fatal(err)
	}
	for _, card := range cards.Items {
		if card.Spec.Image != testCollectionImage {
			t.Errorf("card %q points at %q", card.Name, card.Spec.Image)
		}
	}
}

// A source that loses a skill loses its card, or a stale card keeps pointing
// at a subPath the image no longer has.
func TestMaterializePrunesRemovedSkills(t *testing.T) {
	src := t.TempDir()
	writeSkill(t, filepath.Join(src, skillPlan), skillPlan, nil)
	c := withClient(t,
		existingCard("konveyor-plan", skillPlan),
		existingCard("konveyor-gone", "gone"))

	names, err := Materialize(context.Background(), src, MaterializeOptions{
		Owner: testOwner(),
		Image: testCollectionImage,
	})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if len(names) != 1 {
		t.Fatalf("names = %v", names)
	}

	var cards konveyoriov1alpha1.SkillCardList
	if err := c.List(context.Background(), &cards,
		client.MatchingLabels{labelSkillCollection: srcKonveyor}); err != nil {
		t.Fatal(err)
	}
	if len(cards.Items) != 1 || cards.Items[0].Name != "konveyor-plan" {
		got := make([]string, 0, len(cards.Items))
		for _, x := range cards.Items {
			got = append(got, x.Name)
		}
		t.Fatalf("after pruning: %v, want only konveyor-plan", got)
	}
}

// Pruning is scoped by label, so one collection must not delete another's
// cards even when both enumerate the same image.
func TestMaterializeLeavesOtherCollectionsAlone(t *testing.T) {
	src := t.TempDir()
	writeSkill(t, filepath.Join(src, skillPlan), skillPlan, nil)

	theirs := existingCard("theirs-verify", "verify")
	theirs.Labels[labelSkillCollection] = "theirs"
	c := withClient(t, theirs)

	if _, err := Materialize(context.Background(), src, MaterializeOptions{
		Owner: testOwner(),
		Image: testCollectionImage,
	}); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	var card konveyoriov1alpha1.SkillCard
	if err := c.Get(context.Background(), client.ObjectKey{
		Namespace: testNamespace, Name: "theirs-verify",
	}, &card); err != nil {
		t.Fatalf("another collection's card was pruned: %v", err)
	}
}

// A hand-authored card carries no label, so pruning cannot see it.
func TestMaterializeLeavesHandAuthoredCardsAlone(t *testing.T) {
	src := t.TempDir()
	writeSkill(t, filepath.Join(src, skillPlan), skillPlan, nil)

	handmade := &konveyoriov1alpha1.SkillCard{
		ObjectMeta: metav1.ObjectMeta{Name: "my-own-skill", Namespace: testNamespace},
		Spec:       konveyoriov1alpha1.SkillCardSpec{Image: testCollectionImage},
	}
	c := withClient(t, handmade)

	if _, err := Materialize(context.Background(), src, MaterializeOptions{
		Owner: testOwner(),
		Image: testCollectionImage,
	}); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	var card konveyoriov1alpha1.SkillCard
	if err := c.Get(context.Background(), client.ObjectKey{
		Namespace: testNamespace, Name: "my-own-skill",
	}, &card); err != nil {
		t.Fatalf("a hand-authored card was pruned: %v", err)
	}
}

// Re-running against the same source converges rather than erroring on cards
// it already wrote.
func TestMaterializeIsIdempotent(t *testing.T) {
	src := t.TempDir()
	writeSkill(t, filepath.Join(src, skillPlan), skillPlan, nil)
	withClient(t)

	first, err := Materialize(context.Background(), src, MaterializeOptions{
		Owner: testOwner(), Image: testCollectionImage,
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := Materialize(context.Background(), src, MaterializeOptions{
		Owner: testOwner(), Image: testCollectionImage,
	})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if len(first) != len(second) || first[0] != second[0] {
		t.Errorf("not idempotent: %v then %v", first, second)
	}
}

// An unusable skill must stop the run rather than write a partial set.
func TestMaterializeRejectsAnUnusableSource(t *testing.T) {
	src := t.TempDir()
	writeSkillRaw(t, filepath.Join(src, "broken"), "# no frontmatter\n")
	c := withClient(t)

	if _, err := Materialize(context.Background(), src, MaterializeOptions{
		Owner: testOwner(), Image: testCollectionImage,
	}); err == nil {
		t.Fatal("want an error for an unusable source")
	}

	var cards konveyoriov1alpha1.SkillCardList
	if err := c.List(context.Background(), &cards); err != nil {
		t.Fatal(err)
	}
	if len(cards.Items) != 0 {
		t.Errorf("wrote %d cards despite the source being unusable", len(cards.Items))
	}
}
