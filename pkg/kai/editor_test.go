package kai

import (
	"testing"

	agenticv1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// TestApplyEdit_DeletesFields verifies that a field removed in the editor is
// actually cleared on the updated object, rather than retaining its fetched
// value (the bug when decoding into the populated fetch result).
func TestApplyEdit_DeletesFields(t *testing.T) {
	const (
		editName  = "demo"
		editImage = "img:latest"
	)
	current := &agenticv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{
			Name:            editName,
			Namespace:       "ns",
			ResourceVersion: "42",
		},
		Spec: agenticv1alpha1.AgentSpec{
			Image:  editImage,
			Prompt: "original prompt",
		},
	}

	// Simulate an edit that removed the prompt field entirely.
	edited, err := yaml.Marshal(&agenticv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: editName, Namespace: "ns"},
		Spec:       agenticv1alpha1.AgentSpec{Image: editImage},
	})
	if err != nil {
		t.Fatalf("marshal edited: %v", err)
	}

	updated, err := applyEdit(current, edited)
	if err != nil {
		t.Fatalf("applyEdit: %v", err)
	}
	agent, ok := updated.(*agenticv1alpha1.Agent)
	if !ok {
		t.Fatalf("applyEdit returned %T, want *Agent", updated)
	}
	if agent.Spec.Prompt != "" {
		t.Errorf("prompt = %q, want empty (field was deleted in the edit)", agent.Spec.Prompt)
	}
	if agent.Spec.Image != editImage {
		t.Errorf("image = %q, want %q", agent.Spec.Image, editImage)
	}
	// Identity and resourceVersion must be restored from the fetched object.
	if agent.Name != editName || agent.Namespace != "ns" {
		t.Errorf("identity = %s/%s, want ns/demo", agent.Namespace, agent.Name)
	}
	if agent.ResourceVersion != "42" {
		t.Errorf("resourceVersion = %q, want %q", agent.ResourceVersion, "42")
	}
}
