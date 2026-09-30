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

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

// podWithExit builds a pod whose agent container has terminated with the
// given exit code and termination message.
func podWithExit(code int32, message string) *corev1.Pod {
	return &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: agentContainerName,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: code,
							Message:  message,
						},
					},
				},
			},
		},
	}
}

func TestSetTerminalOutcome(t *testing.T) {
	r := &AgentRunReconciler{}

	cases := []struct {
		name           string
		pod            *corev1.Pod
		sandboxReason  string
		failureMessage string
		wantPhase      konveyoriov1alpha1.AgentRunPhase
		wantStatus     metav1.ConditionStatus
		wantReason     string
		wantMessage    string
	}{
		{
			name:       "exit 0 is clean success",
			pod:        podWithExit(0, ""),
			wantPhase:  konveyoriov1alpha1.AgentRunPhaseSucceeded,
			wantStatus: metav1.ConditionTrue,
			wantReason: konveyoriov1alpha1.AgentRunReasonSucceeded,
		},
		{
			name:       "exit 1 is failure",
			pod:        podWithExit(1, ""),
			wantPhase:  konveyoriov1alpha1.AgentRunPhaseFailed,
			wantStatus: metav1.ConditionFalse,
			wantReason: konveyoriov1alpha1.AgentRunReasonFailed,
		},
		{
			name:           "failure prefers the harness termination message (#143)",
			pod:            podWithExit(1, ""),
			failureMessage: "source is not a git repository",
			wantPhase:      konveyoriov1alpha1.AgentRunPhaseFailed,
			wantStatus:     metav1.ConditionFalse,
			wantReason:     konveyoriov1alpha1.AgentRunReasonFailed,
			wantMessage:    "source is not a git repository",
		},
		{
			name:       "exit 2 is limit reached (remapped from failed pod)",
			pod:        podWithExit(2, ""),
			wantPhase:  konveyoriov1alpha1.AgentRunPhaseFailed,
			wantStatus: metav1.ConditionFalse,
			wantReason: konveyoriov1alpha1.AgentRunReasonLimitReached,
		},
		{
			// #241: the agent's own handoff said the stage did not do its
			// work. The termination message is the harness's JSON blob and
			// must not become the condition message; the condition names
			// the outcome, the blob carries the detail.
			name:           "exit 3 is refused, with a fixed message",
			pod:            podWithExit(3, `{"exitCode":3,"outcome":"refused","stopReason":"handoff: docs/plan.md not found"}`),
			failureMessage: `{"exitCode":3,"outcome":"refused","stopReason":"handoff: docs/plan.md not found"}`,
			wantPhase:      konveyoriov1alpha1.AgentRunPhaseFailed,
			wantStatus:     metav1.ConditionFalse,
			wantReason:     konveyoriov1alpha1.AgentRunReasonRefused,
			wantMessage:    "The agent's handoff records that the stage did not do its work; .konveyor/handoff.md on the run's branch says why",
		},
		{
			name:          "no exit code falls back to sandbox PodSucceeded",
			pod:           nil,
			sandboxReason: sandboxFinishedReasonSucceeded,
			wantPhase:     konveyoriov1alpha1.AgentRunPhaseSucceeded,
			wantStatus:    metav1.ConditionTrue,
			wantReason:    konveyoriov1alpha1.AgentRunReasonSucceeded,
		},
		{
			name:          "no exit code, non-success sandbox reason is failure",
			pod:           nil,
			sandboxReason: "PodFailed",
			wantPhase:     konveyoriov1alpha1.AgentRunPhaseFailed,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    konveyoriov1alpha1.AgentRunReasonFailed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := &konveyoriov1alpha1.AgentRun{}
			r.setTerminalOutcome(run, tc.pod, tc.sandboxReason, tc.failureMessage)

			if run.Status.Phase != tc.wantPhase {
				t.Errorf("phase = %q, want %q", run.Status.Phase, tc.wantPhase)
			}
			succeeded := meta.FindStatusCondition(run.Status.Conditions, konveyoriov1alpha1.AgentRunConditionSucceeded)
			if succeeded == nil {
				t.Fatalf("Succeeded condition not set")
			}
			if succeeded.Status != tc.wantStatus {
				t.Errorf("Succeeded.Status = %q, want %q", succeeded.Status, tc.wantStatus)
			}
			if succeeded.Reason != tc.wantReason {
				t.Errorf("Succeeded.Reason = %q, want %q", succeeded.Reason, tc.wantReason)
			}
			if tc.wantMessage != "" && succeeded.Message != tc.wantMessage {
				t.Errorf("Succeeded.Message = %q, want %q", succeeded.Message, tc.wantMessage)
			}
			// AgentRun carries no Ready condition (ADR 0018) — the
			// terminal outcome lives only on Succeeded.
			if ready := meta.FindStatusCondition(run.Status.Conditions, ConditionTypeReady); ready != nil {
				t.Errorf("AgentRun should not set a Ready condition, got %+v", ready)
			}
		})
	}
}

func TestAgentExitCode(t *testing.T) {
	if _, ok := agentExitCode(nil); ok {
		t.Errorf("nil pod should report no exit code")
	}
	if code, ok := agentExitCode(podWithExit(2, "")); !ok || code != 2 {
		t.Errorf("got (%d,%v), want (2,true)", code, ok)
	}
	// A pod whose agent container has not terminated yields no code.
	running := &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
		{Name: agentContainerName, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
	}}}
	if _, ok := agentExitCode(running); ok {
		t.Errorf("running container should report no exit code")
	}
}

func TestTerminationDataFromPod(t *testing.T) {
	cases := []struct {
		name    string
		message string
		wantSet bool
	}{
		{"json object is captured", `{"cost":{"amount":1.5},"turnsUsed":10}`, true},
		{"json array is rejected (CRD wants an object)", `[1,2,3]`, false},
		{"bare scalar is rejected", `42`, false},
		{"bare string is rejected", `"done"`, false},
		{"invalid json is rejected", `{not json`, false},
		{"empty message yields nothing", ``, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			td := terminationDataFromPod(podWithExit(0, tc.message))
			if tc.wantSet {
				if td == nil {
					t.Fatalf("expected terminationData to be set for %q", tc.message)
				}
				if string(td.Raw) != tc.message {
					t.Errorf("terminationData = %q, want verbatim %q", td.Raw, tc.message)
				}
			} else if td != nil {
				t.Errorf("expected no terminationData for %q, got %q", tc.message, td.Raw)
			}
		})
	}

	// No pod, or a pod whose agent container has not terminated, yields nothing.
	if terminationDataFromPod(nil) != nil {
		t.Errorf("nil pod should yield no terminationData")
	}
}
