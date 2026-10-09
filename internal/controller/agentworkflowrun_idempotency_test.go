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
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

const idempotencyWorkflowRunName = "workflow-run"

func workflowRunTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := konveyoriov1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add controller API scheme: %v", err)
	}
	return scheme
}

func TestCreateAgentRunForStageAdoptsOwnedExistingChild(t *testing.T) {
	scheme := workflowRunTestScheme(t)
	parent := &konveyoriov1alpha1.AgentWorkflowRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      idempotencyWorkflowRunName,
			Namespace: testNamespace,
			UID:       types.UID("workflow-run-uid"),
		},
	}
	agent := &konveyoriov1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: scopeAgent, Namespace: parent.Namespace},
	}
	stage := &konveyoriov1alpha1.AgentWorkflowStage{
		Name:     stageAName,
		AgentRef: agent.Name,
	}
	childName := stageAgentRunName(parent.Name, stage.Name)
	controller := true
	existing := &konveyoriov1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      childName,
			Namespace: parent.Namespace,
			UID:       types.UID("existing-child-uid"),
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         konveyoriov1alpha1.GroupVersion.String(),
				Kind:               "AgentWorkflowRun",
				Name:               parent.Name,
				UID:                parent.UID,
				Controller:         &controller,
				BlockOwnerDeletion: &controller,
			}},
		},
		Spec: konveyoriov1alpha1.AgentRunSpec{AgentRef: agent.Name},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(parent, agent, existing).Build()
	r := &AgentWorkflowRunReconciler{Client: c, Scheme: scheme}

	got, err := r.createAgentRunForStage(context.Background(), parent, stage, 0, 1)
	if err != nil {
		t.Fatalf("createAgentRunForStage returned error: %v", err)
	}
	if got != childName {
		t.Fatalf("child name = %q, want %q", got, childName)
	}
	if _, err := r.createAgentRunForStage(context.Background(), parent, stage, 0, 1); err != nil {
		t.Fatalf("retrying createAgentRunForStage returned error: %v", err)
	}

	var children konveyoriov1alpha1.AgentRunList
	if err := c.List(context.Background(), &children, client.InNamespace(parent.Namespace)); err != nil {
		t.Fatalf("list AgentRuns: %v", err)
	}
	if len(children.Items) != 1 {
		t.Fatalf("got %d AgentRuns, want exactly one", len(children.Items))
	}
	if children.Items[0].UID != existing.UID {
		t.Errorf("existing child UID changed to %q; idempotent adoption must preserve it", children.Items[0].UID)
	}
}

func TestCreateAgentRunForStageRejectsForeignExistingChild(t *testing.T) {
	scheme := workflowRunTestScheme(t)
	parent := &konveyoriov1alpha1.AgentWorkflowRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      idempotencyWorkflowRunName,
			Namespace: testNamespace,
			UID:       types.UID("workflow-run-uid"),
		},
	}
	agent := &konveyoriov1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: scopeAgent, Namespace: parent.Namespace},
	}
	stage := &konveyoriov1alpha1.AgentWorkflowStage{
		Name:     stageAName,
		AgentRef: agent.Name,
	}
	foreign := &konveyoriov1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      stageAgentRunName(parent.Name, stage.Name),
			Namespace: parent.Namespace,
		},
		Spec: konveyoriov1alpha1.AgentRunSpec{AgentRef: agent.Name},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(parent, agent, foreign).Build()
	r := &AgentWorkflowRunReconciler{Client: c, Scheme: scheme}

	_, err := r.createAgentRunForStage(context.Background(), parent, stage, 0, 1)
	if err == nil {
		t.Fatal("createAgentRunForStage succeeded for a foreign existing child")
	}
	if !strings.Contains(err.Error(), "already exists but is not owned") {
		t.Fatalf("error = %q, want foreign-owner diagnostic", err)
	}
}

func TestReconcileAdoptsExistingStageAfterStatusLoss(t *testing.T) {
	scheme := workflowRunTestScheme(t)
	parent := &konveyoriov1alpha1.AgentWorkflowRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      idempotencyWorkflowRunName,
			Namespace: testNamespace,
			UID:       types.UID("workflow-run-uid"),
		},
		Spec: konveyoriov1alpha1.AgentWorkflowRunSpec{
			WorkflowRef: scopeWorkflow,
		},
	}
	workflow := &konveyoriov1alpha1.AgentWorkflow{
		ObjectMeta: metav1.ObjectMeta{Name: scopeWorkflow, Namespace: parent.Namespace},
		Spec: konveyoriov1alpha1.AgentWorkflowSpec{
			Stages: []konveyoriov1alpha1.AgentWorkflowStage{{
				Name:     stageAName,
				AgentRef: scopeAgent,
			}},
		},
		Status: konveyoriov1alpha1.AgentWorkflowStatus{},
	}
	meta.SetStatusCondition(&workflow.Status.Conditions, metav1.Condition{
		Type:   ConditionTypeReady,
		Status: metav1.ConditionTrue,
		Reason: reasonSucceeded,
	})
	agent := &konveyoriov1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: scopeAgent, Namespace: parent.Namespace},
	}
	controller := true
	child := &konveyoriov1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      stageAgentRunName(parent.Name, stageAName),
			Namespace: parent.Namespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         konveyoriov1alpha1.GroupVersion.String(),
				Kind:               "AgentWorkflowRun",
				Name:               parent.Name,
				UID:                parent.UID,
				Controller:         &controller,
				BlockOwnerDeletion: &controller,
			}},
		},
		Spec: konveyoriov1alpha1.AgentRunSpec{AgentRef: agent.Name},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(parent).
		WithObjects(parent, workflow, agent, child).
		Build()
	r := &AgentWorkflowRunReconciler{Client: c, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: parent.Name, Namespace: parent.Namespace},
	})
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	var fetched konveyoriov1alpha1.AgentWorkflowRun
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(parent), &fetched); err != nil {
		t.Fatalf("fetch workflow run: %v", err)
	}
	if len(fetched.Status.Stages) != 1 {
		t.Fatalf("got %d stage statuses, want one", len(fetched.Status.Stages))
	}
	if fetched.Status.Stages[0].AgentRunName != child.Name {
		t.Errorf("recorded child = %q, want %q", fetched.Status.Stages[0].AgentRunName, child.Name)
	}
	if fetched.Status.Stages[0].Phase != konveyoriov1alpha1.AgentRunPhasePending {
		t.Errorf("stage phase = %q, want Pending", fetched.Status.Stages[0].Phase)
	}

	var children konveyoriov1alpha1.AgentRunList
	if err := c.List(context.Background(), &children, client.InNamespace(parent.Namespace)); err != nil {
		t.Fatalf("list AgentRuns: %v", err)
	}
	if len(children.Items) != 1 {
		t.Fatalf("got %d AgentRuns after reconciliation, want exactly one", len(children.Items))
	}
}
