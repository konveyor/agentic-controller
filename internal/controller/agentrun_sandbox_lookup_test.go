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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

const lookupSandboxName = "lookup-run"

// TestSandboxLookupConfirmsCacheMiss covers the read of the Sandbox a run
// already recorded in status.sandboxName. Failing the run there is terminal
// and its spec is immutable, so a miss in the manager cache must be confirmed
// against the API server first: the reconcile that records the name is
// triggered by the controller's own status patch, which can outrun the
// Sandbox informer. Without the confirmation a run whose sandbox pod goes on
// to finish cleanly is still reported Failed.
func TestSandboxLookupConfirmsCacheMiss(t *testing.T) {
	for _, tc := range []struct {
		name        string
		onAPIServer bool
		wantRequeue bool
		wantPhase   konveyoriov1alpha1.AgentRunPhase
	}{
		{
			name:        "cache lags the Sandbox it just created",
			onAPIServer: true,
			wantRequeue: true,
			wantPhase:   konveyoriov1alpha1.AgentRunPhasePending,
		},
		{
			name:        "Sandbox really is gone",
			onAPIServer: false,
			wantPhase:   konveyoriov1alpha1.AgentRunPhaseFailed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			for _, add := range []func(*runtime.Scheme) error{
				corev1.AddToScheme, konveyoriov1alpha1.AddToScheme, sandboxv1beta1.AddToScheme,
			} {
				if err := add(scheme); err != nil {
					t.Fatal(err)
				}
			}

			agent := &konveyoriov1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: testAgentName, Namespace: testNamespace},
				Status: konveyoriov1alpha1.AgentStatus{Conditions: []metav1.Condition{
					{Type: ConditionTypeReady, Status: metav1.ConditionTrue, Reason: "Ready",
						LastTransitionTime: metav1.Now()},
				}},
			}
			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{Name: lookupSandboxName, Namespace: testNamespace},
				Spec:       konveyoriov1alpha1.AgentRunSpec{AgentRef: testAgentName},
				Status: konveyoriov1alpha1.AgentRunStatus{
					Phase:       konveyoriov1alpha1.AgentRunPhasePending,
					SandboxName: lookupSandboxName,
				},
			}
			sandbox := &sandboxv1beta1.Sandbox{
				ObjectMeta: metav1.ObjectMeta{Name: lookupSandboxName, Namespace: testNamespace},
			}

			// The manager cache has not seen the Sandbox; the API server has,
			// unless this case is a real deletion.
			cached := fake.NewClientBuilder().WithScheme(scheme).
				WithStatusSubresource(run).WithObjects(agent, run).Build()
			apiObjects := []client.Object{agent, run.DeepCopy()}
			if tc.onAPIServer {
				apiObjects = append(apiObjects, sandbox)
			}
			apiServer := fake.NewClientBuilder().WithScheme(scheme).WithObjects(apiObjects...).Build()

			r := &AgentRunReconciler{Client: cached, Scheme: scheme, apiReader: apiServer}
			result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)})
			if err != nil {
				t.Fatal(err)
			}
			// A confirmed cache miss asks to be reconciled again rather than
			// acting on the stale view; a real deletion is settled.
			if result.IsZero() == tc.wantRequeue {
				t.Errorf("result = %+v, want requeue = %v", result, tc.wantRequeue)
			}

			var got konveyoriov1alpha1.AgentRun
			if err := cached.Get(ctx, client.ObjectKeyFromObject(run), &got); err != nil {
				t.Fatal(err)
			}
			if got.Status.Phase != tc.wantPhase {
				t.Errorf("phase = %q, want %q", got.Status.Phase, tc.wantPhase)
			}
			succeeded := meta.FindStatusCondition(got.Status.Conditions,
				konveyoriov1alpha1.AgentRunConditionSucceeded)
			if tc.wantPhase == konveyoriov1alpha1.AgentRunPhaseFailed {
				if succeeded == nil || succeeded.Reason != "SandboxNotFound" {
					t.Errorf("Succeeded condition = %+v, want reason SandboxNotFound", succeeded)
				}
			} else if succeeded != nil && succeeded.Reason == "SandboxNotFound" {
				t.Error("run failed with SandboxNotFound on a cache miss the API server contradicts")
			}
		})
	}
}
