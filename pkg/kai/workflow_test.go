package kai

import (
	"context"
	"testing"

	agenticv1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestStageNameValidator(t *testing.T) {
	existing := []agenticv1alpha1.AgentWorkflowStage{{Name: "analyze"}}
	validate := stageNameValidator(existing)

	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"ok", "triage", false},
		{"ok with hyphen/dot", "stage-1.2", false},
		{"empty", "", true},
		{"uppercase", "Analyze", true},
		{"underscore", "my_stage", true},
		{"leading hyphen", "-stage", true},
		{"duplicate", "analyze", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validate(tt.in); (err != nil) != tt.wantErr {
				t.Errorf("stageNameValidator(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
		})
	}
}

func TestValidateWorkflowParams(t *testing.T) {
	const (
		ns          = "ns"
		priorityKey = "priority"
	)
	agent := &agenticv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "triager", Namespace: ns},
		Spec: agenticv1alpha1.AgentSpec{
			Params: []agenticv1alpha1.Param{{Name: "issue", Type: paramTypeString}},
		},
	}
	wf := &agenticv1alpha1.AgentWorkflow{
		ObjectMeta: metav1.ObjectMeta{Name: "triage", Namespace: ns},
		Spec: agenticv1alpha1.AgentWorkflowSpec{
			Params: []agenticv1alpha1.Param{{Name: priorityKey, Type: paramTypeString}},
			Stages: []agenticv1alpha1.AgentWorkflowStage{{Name: "s1", AgentRef: "triager"}},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(agent).Build()
	ctx := context.Background()

	// A workflow-level param and a stage-Agent param are both accepted.
	if err := validateWorkflowParams(ctx, cl, ns, wf, map[string]string{priorityKey: "high", "issue": "7"}); err != nil {
		t.Errorf("expected declared params to be accepted, got %v", err)
	}
	// An undeclared param is rejected.
	if err := validateWorkflowParams(ctx, cl, ns, wf, map[string]string{"typo": "x"}); err == nil {
		t.Error("expected error for an undeclared workflow parameter")
	}
	// A missing stage Agent surfaces an error rather than silently passing.
	wfMissing := wf.DeepCopy()
	wfMissing.Spec.Stages = []agenticv1alpha1.AgentWorkflowStage{{Name: "s1", AgentRef: "ghost"}}
	if err := validateWorkflowParams(ctx, cl, ns, wfMissing, map[string]string{priorityKey: "high"}); err == nil {
		t.Error("expected error when a stage Agent cannot be fetched")
	}
}
