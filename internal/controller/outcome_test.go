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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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
			sandboxReason: testSandboxFinishedReasonFailed,
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

const (
	// testPodNamespace is the namespace the terminal-pod tests build in.
	testPodNamespace = "default"
	// testSandboxFinishedReasonFailed is the Sandbox Finished reason for
	// a pod that did not succeed, the counterpart to the production
	// sandboxFinishedReasonSucceeded.
	testSandboxFinishedReasonFailed = "PodFailed"
)

// podNamed builds a pod whose agent container has terminated with the
// given exit code and message. The pod is named after its Sandbox, as
// Agent Sandbox names it.
func podNamed(name string, code int32, message string) *corev1.Pod {
	pod := podWithExit(code, message)
	pod.Name = name
	pod.Namespace = testPodNamespace
	return pod
}

// finishedSandbox builds a Sandbox reporting Finished with the given
// reason.
func finishedSandbox(name, reason string) *sandboxv1beta1.Sandbox {
	return &sandboxv1beta1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testPodNamespace},
		Status: sandboxv1beta1.SandboxStatus{
			Conditions: []metav1.Condition{{
				Type:               sandboxConditionFinished,
				Status:             metav1.ConditionTrue,
				Reason:             reason,
				LastTransitionTime: metav1.Now(),
			}},
		},
	}
}

// podReader builds a direct reader serving the given pods, standing in
// for the API server behind the manager cache.
func podReader(t *testing.T, pods ...*corev1.Pod) client.Reader {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1 to scheme: %v", err)
	}
	objs := make([]client.Object, 0, len(pods))
	for _, p := range pods {
		objs = append(objs, p)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

// The label-restricted Pod cache can lag the Sandbox's Finished
// condition, and a terminal run takes the TTL shortcut at the top of
// Reconcile and is never revisited. So a cached pod that has not caught
// up does not delay the run's terminal facts, it loses them: the measured
// verification verdict is recorded as VerificationNotReported for good,
// and an exit 2 or 3 reads as a plain success. The terminal facts are
// therefore derived from a direct read when the cached pod has no
// termination (djzager on #309).
func TestTerminalFactsSurviveAStalePodCache(t *testing.T) {
	const (
		runName = "ar-stale-cache"
		ns      = testPodNamespace
		// The #300 dogfooding blob: the agent claims a pass, the harness
		// measured a type check that did not.
		blobJSON = `{"exitCode":0,"outcome":"succeeded","handoffStatus":"passed",` +
			`"verification":{"status":"failed","reason":"Build failed: npm run tsc -w client",` +
			`"commit":"4f2a1c9e7b3041d8a6c25e90b17f3ac4d8e6b215"}}`
	)

	// The cached pod exists but its status has not caught up: no container
	// statuses at all, so no exit code and no termination message.
	stale := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: runName, Namespace: ns}}

	cases := []struct {
		name string
		// cached is the pod the manager cache serves, as Reconcile would
		// pass it; nil means the cache does not have the pod yet.
		cached *corev1.Pod
		// served is what the API server holds; nil means the pod is gone.
		served        *corev1.Pod
		noDirectRead  bool
		sandboxReason string

		wantPhase    konveyoriov1alpha1.AgentRunPhase
		wantReason   string
		wantVerified metav1.ConditionStatus
		wantVerifiedReason,
		wantTerminationData string
	}{
		{
			name:                "stale cached pod, terminated pod served directly",
			cached:              stale,
			served:              podNamed(runName, 0, blobJSON),
			sandboxReason:       sandboxFinishedReasonSucceeded,
			wantPhase:           konveyoriov1alpha1.AgentRunPhaseSucceeded,
			wantReason:          konveyoriov1alpha1.AgentRunReasonSucceeded,
			wantVerified:        metav1.ConditionFalse,
			wantVerifiedReason:  konveyoriov1alpha1.AgentRunReasonVerificationFailed,
			wantTerminationData: blobJSON,
		},
		{
			// The same race costs the exit code, not just the verdict: a
			// run that hit its budget would read as a clean success.
			name:               "stale cached pod loses neither the verdict nor the exit code",
			cached:             stale,
			served:             podNamed(runName, harnessExitLimitReached, blobJSON),
			sandboxReason:      sandboxFinishedReasonSucceeded,
			wantPhase:          konveyoriov1alpha1.AgentRunPhaseFailed,
			wantReason:         konveyoriov1alpha1.AgentRunReasonLimitReached,
			wantVerified:       metav1.ConditionFalse,
			wantVerifiedReason: konveyoriov1alpha1.AgentRunReasonVerificationFailed,
		},
		{
			// The cache may not hold the pod at all yet.
			name:               "pod absent from the cache entirely",
			cached:             nil,
			served:             podNamed(runName, 0, blobJSON),
			sandboxReason:      sandboxFinishedReasonSucceeded,
			wantPhase:          konveyoriov1alpha1.AgentRunPhaseSucceeded,
			wantReason:         konveyoriov1alpha1.AgentRunReasonSucceeded,
			wantVerified:       metav1.ConditionFalse,
			wantVerifiedReason: konveyoriov1alpha1.AgentRunReasonVerificationFailed,
		},
		{
			// The cache has caught up: the direct read is not needed, and
			// the cached pod's own record is what is used. The reader
			// serves a contradicting pod to prove it is not consulted.
			name:               "a caught-up cache is used as is",
			cached:             podNamed(runName, 0, blobJSON),
			served:             podNamed(runName, harnessExitRefused, `{"verification":{"status":"passed"}}`),
			sandboxReason:      sandboxFinishedReasonSucceeded,
			wantPhase:          konveyoriov1alpha1.AgentRunPhaseSucceeded,
			wantReason:         konveyoriov1alpha1.AgentRunReasonSucceeded,
			wantVerified:       metav1.ConditionFalse,
			wantVerifiedReason: konveyoriov1alpha1.AgentRunReasonVerificationFailed,
		},
		{
			// Nothing to wait for: the pod is gone. The run still reaches
			// a terminal state from the Sandbox's own reason, and the
			// unreadable verdict is Unknown — never a pass.
			name:               "pod gone from the API server falls back to the sandbox reason",
			cached:             stale,
			served:             nil,
			sandboxReason:      sandboxFinishedReasonSucceeded,
			wantPhase:          konveyoriov1alpha1.AgentRunPhaseSucceeded,
			wantReason:         konveyoriov1alpha1.AgentRunReasonSucceeded,
			wantVerified:       metav1.ConditionUnknown,
			wantVerifiedReason: konveyoriov1alpha1.AgentRunReasonVerificationNotReported,
		},
		{
			// And the fallback carries the Sandbox's own reason through,
			// rather than defaulting a vanished pod to success.
			name:               "pod gone with a failed sandbox reports the failure",
			cached:             stale,
			served:             nil,
			sandboxReason:      testSandboxFinishedReasonFailed,
			wantPhase:          konveyoriov1alpha1.AgentRunPhaseFailed,
			wantReason:         konveyoriov1alpha1.AgentRunReasonFailed,
			wantVerified:       metav1.ConditionUnknown,
			wantVerifiedReason: konveyoriov1alpha1.AgentRunReasonVerificationNotReported,
		},
		{
			// A reconciler with no direct reader (a zero value, as unit
			// tests build) must still report, not panic.
			name:               "no direct reader still reports a terminal run",
			cached:             stale,
			noDirectRead:       true,
			sandboxReason:      sandboxFinishedReasonSucceeded,
			wantPhase:          konveyoriov1alpha1.AgentRunPhaseSucceeded,
			wantReason:         konveyoriov1alpha1.AgentRunReasonSucceeded,
			wantVerified:       metav1.ConditionUnknown,
			wantVerifiedReason: konveyoriov1alpha1.AgentRunReasonVerificationNotReported,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &AgentRunReconciler{}
			if !tc.noDirectRead {
				if tc.served != nil {
					r.apiReader = podReader(t, tc.served)
				} else {
					r.apiReader = podReader(t)
				}
			}

			run := &konveyoriov1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{Name: runName, Namespace: ns},
				Status:     konveyoriov1alpha1.AgentRunStatus{SandboxName: runName},
			}
			sandbox := finishedSandbox(runName, tc.sandboxReason)

			if requeue := r.updatePhaseFromSandbox(context.Background(), run, sandbox, tc.cached); requeue != 0 {
				t.Errorf("requeueAfter = %v, want 0 for a finished sandbox", requeue)
			}

			if run.Status.Phase != tc.wantPhase {
				t.Errorf("phase = %q, want %q", run.Status.Phase, tc.wantPhase)
			}
			succeeded := meta.FindStatusCondition(run.Status.Conditions,
				konveyoriov1alpha1.AgentRunConditionSucceeded)
			if succeeded == nil {
				t.Fatalf("Succeeded condition not set")
			}
			if succeeded.Reason != tc.wantReason {
				t.Errorf("Succeeded.Reason = %q, want %q", succeeded.Reason, tc.wantReason)
			}

			verified := meta.FindStatusCondition(run.Status.Conditions,
				konveyoriov1alpha1.AgentRunConditionVerified)
			if verified == nil {
				t.Fatalf("Verified condition not set")
			}
			if verified.Status != tc.wantVerified {
				t.Errorf("Verified.Status = %q, want %q", verified.Status, tc.wantVerified)
			}
			if verified.Reason != tc.wantVerifiedReason {
				t.Errorf("Verified.Reason = %q, want %q", verified.Reason, tc.wantVerifiedReason)
			}

			if tc.wantTerminationData != "" {
				if run.Status.TerminationData == nil {
					t.Fatalf("terminationData is nil; the blob from the direct read should be stored")
				}
				if got := string(run.Status.TerminationData.Raw); got != tc.wantTerminationData {
					t.Errorf("terminationData = %q, want %q", got, tc.wantTerminationData)
				}
			}
			if run.Status.CompletionTime == nil {
				t.Errorf("completionTime not stamped on a finished run")
			}
		})
	}
}

// terminalPod is the narrow decision behind the test above: swap in a
// directly-read pod only when the cached one has nothing to say.
func TestTerminalPodPrefersTheRecordThatHasATermination(t *testing.T) {
	const (
		name = "ar-terminal-pod"
		ns   = testPodNamespace
	)
	stale := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
	sandbox := finishedSandbox(name, sandboxFinishedReasonSucceeded)
	served := podNamed(name, 7, `{"outcome":"failed"}`)

	t.Run("cached termination wins without a read", func(t *testing.T) {
		cached := podNamed(name, 0, `{"outcome":"succeeded"}`)
		r := &AgentRunReconciler{apiReader: podReader(t, served)}
		got := r.terminalPod(context.Background(), cached, sandbox)
		if got != cached {
			t.Errorf("terminalPod returned a different pod; a cached termination needs no direct read")
		}
	})

	t.Run("stale cache yields to the served pod", func(t *testing.T) {
		r := &AgentRunReconciler{apiReader: podReader(t, served)}
		got := r.terminalPod(context.Background(), stale, sandbox)
		term := agentContainerTermination(got)
		if term == nil {
			t.Fatalf("terminalPod returned a pod with no termination")
		}
		if term.ExitCode != 7 {
			t.Errorf("exit code = %d, want 7 from the directly-read pod", term.ExitCode)
		}
	})

	t.Run("neither has a termination, the cached pod is kept", func(t *testing.T) {
		unterminated := podNamed(name, 0, "")
		unterminated.Status.ContainerStatuses = nil
		r := &AgentRunReconciler{apiReader: podReader(t, unterminated)}
		if got := r.terminalPod(context.Background(), stale, sandbox); got != stale {
			t.Errorf("terminalPod swapped in a pod that says no more than the cached one")
		}
	})

	t.Run("a nil cached pod is replaced by the served one", func(t *testing.T) {
		r := &AgentRunReconciler{apiReader: podReader(t, served)}
		if got := r.terminalPod(context.Background(), nil, sandbox); got == nil {
			t.Errorf("terminalPod returned nil with a pod available to read")
		}
	})

	t.Run("no reader is not a panic", func(t *testing.T) {
		r := &AgentRunReconciler{}
		if got := r.terminalPod(context.Background(), stale, sandbox); got != stale {
			t.Errorf("terminalPod should return the cached pod with no reader configured")
		}
	})
}
