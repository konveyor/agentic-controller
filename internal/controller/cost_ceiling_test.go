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

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

func TestSetRunCostCeiling(t *testing.T) {
	tests := []struct {
		name       string
		td         *runtime.RawExtension
		wantStatus metav1.ConditionStatus
		wantReason string
	}{
		{
			name:       "enforced",
			td:         blob(`{"exitCode":0,"costCeiling":"enforced"}`),
			wantStatus: metav1.ConditionTrue,
			wantReason: konveyoriov1alpha1.AgentRunReasonCostReported,
		},
		{
			name:       "unenforced",
			td:         blob(`{"exitCode":0,"costCeiling":"unenforced"}`),
			wantStatus: metav1.ConditionFalse,
			wantReason: konveyoriov1alpha1.AgentRunReasonCostNotReported,
		},
		{name: "no termination data", td: nil},
		{name: "no ceiling configured", td: blob(`{"exitCode":0}`)},
		{name: "unrecognised value", td: blob(`{"costCeiling":"sometimes"}`)},
		{name: "malformed value", td: blob(`{"costCeiling":7}`)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			run := &konveyoriov1alpha1.AgentRun{}
			run.Generation = 3
			setRunCostCeiling(run, tc.td)

			got := meta.FindStatusCondition(run.Status.Conditions,
				konveyoriov1alpha1.AgentRunConditionCostCeilingEnforced)
			if tc.wantReason == "" {
				if got != nil {
					t.Fatalf("expected no condition, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected a CostCeilingEnforced condition")
			}
			if got.Status != tc.wantStatus || got.Reason != tc.wantReason {
				t.Errorf("condition = %s/%s, want %s/%s", got.Status, got.Reason, tc.wantStatus, tc.wantReason)
			}
			if got.Message == "" {
				t.Error("condition message must say what happened")
			}
			if got.ObservedGeneration != 3 {
				t.Errorf("ObservedGeneration = %d, want 3", got.ObservedGeneration)
			}
		})
	}
}

// A malformed costCeiling must not cost the run its verification verdict,
// which is read from the same blob.
func TestMalformedCostCeilingKeepsVerifiedVerdict(t *testing.T) {
	td := blob(`{"costCeiling":7,"verification":{"status":"passed"}}`)
	run := &konveyoriov1alpha1.AgentRun{}
	setRunVerified(run, td)
	setRunCostCeiling(run, td)

	got := meta.FindStatusCondition(run.Status.Conditions, konveyoriov1alpha1.AgentRunConditionVerified)
	if got == nil || got.Status != metav1.ConditionTrue {
		t.Errorf("Verified = %+v, want True", got)
	}
}
