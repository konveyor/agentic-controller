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
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

// blob wraps a harness termination message as the controller stores it.
func blob(raw string) *runtime.RawExtension {
	return &runtime.RawExtension{Raw: []byte(raw)}
}

func TestSetRunVerified(t *testing.T) {
	// The #300 dogfooding case: the stage's own handoff says it passed,
	// the harness measured a type check that did not.
	const measuredFailure = `{
		"exitCode": 0,
		"outcome": "succeeded",
		"handoffStatus": "passed",
		"verification": {
			"status": "failed",
			"reason": "Build failed: npm run tsc -w client",
			"commands": [
				{"label":"Build","command":"npm run tsc -w client","status":"failed","exitCode":2,"seconds":41.2,"output":"45 errors"},
				{"label":"Test","command":"npm run test -w client","status":"passed","exitCode":0,"seconds":93.8}
			]
		}
	}`

	cases := []struct {
		name string
		td   *runtime.RawExtension

		wantStatus metav1.ConditionStatus
		wantReason string
		// wantInMessage, when set, must appear in the condition message.
		wantInMessage string
	}{
		{
			name:          "measured pass",
			td:            blob(`{"verification":{"status":"passed","commands":[{"label":"Build","status":"passed","exitCode":0}]}}`),
			wantStatus:    metav1.ConditionTrue,
			wantReason:    konveyoriov1alpha1.AgentRunReasonVerificationPassed,
			wantInMessage: "exited 0",
		},
		{
			name:          "measured failure carries the harness reason",
			td:            blob(measuredFailure),
			wantStatus:    metav1.ConditionFalse,
			wantReason:    konveyoriov1alpha1.AgentRunReasonVerificationFailed,
			wantInMessage: "Build failed: npm run tsc -w client",
		},
		{
			name:          "notRun is a False, not a pass and not silence",
			td:            blob(`{"verification":{"status":"notRun","reason":"docs/plan.md has no ## Verification section"}}`),
			wantStatus:    metav1.ConditionFalse,
			wantReason:    konveyoriov1alpha1.AgentRunReasonVerificationNotRun,
			wantInMessage: "no ## Verification section",
		},
		{
			name:       "notRun with no reason still reports notRun",
			td:         blob(`{"verification":{"status":"notRun"}}`),
			wantStatus: metav1.ConditionFalse,
			wantReason: konveyoriov1alpha1.AgentRunReasonVerificationNotRun,
		},
		{
			// A harness predating #300: a complete, valid usage blob with
			// no verification key at all. Must not read as verified.
			name:       "older harness reporting no verification is Unknown",
			td:         blob(`{"exitCode":0,"outcome":"succeeded","usage":{"totalTokens":1234}}`),
			wantStatus: metav1.ConditionUnknown,
			wantReason: konveyoriov1alpha1.AgentRunReasonVerificationNotReported,
		},
		{
			name:       "no termination data at all is Unknown",
			td:         nil,
			wantStatus: metav1.ConditionUnknown,
			wantReason: konveyoriov1alpha1.AgentRunReasonVerificationNotReported,
		},
		{
			name:       "empty raw is Unknown",
			td:         &runtime.RawExtension{},
			wantStatus: metav1.ConditionUnknown,
			wantReason: konveyoriov1alpha1.AgentRunReasonVerificationNotReported,
		},
		{
			name:       "unparseable blob is Unknown, never an error",
			td:         blob(`{"verification": not json`),
			wantStatus: metav1.ConditionUnknown,
			wantReason: konveyoriov1alpha1.AgentRunReasonVerificationNotReported,
		},
		{
			name:       "verification present but status empty is Unknown",
			td:         blob(`{"verification":{"reason":"something"}}`),
			wantStatus: metav1.ConditionUnknown,
			wantReason: konveyoriov1alpha1.AgentRunReasonVerificationNotReported,
		},
		{
			name:       "null verification is Unknown",
			td:         blob(`{"verification":null}`),
			wantStatus: metav1.ConditionUnknown,
			wantReason: konveyoriov1alpha1.AgentRunReasonVerificationNotReported,
		},
		{
			// A newer harness inventing a status this controller does not
			// know must degrade to "cannot say", never to a pass.
			name:          "unrecognised status is Unknown, not True",
			td:            blob(`{"verification":{"status":"partiallyPassed"}}`),
			wantStatus:    metav1.ConditionUnknown,
			wantReason:    konveyoriov1alpha1.AgentRunReasonVerificationNotReported,
			wantInMessage: `"partiallyPassed"`,
		},
		{
			// Status values are compared exactly; the harness emits
			// lowerCamelCase and nothing else should be read as a pass.
			name:       "case variant of passed is not a pass",
			td:         blob(`{"verification":{"status":"Passed"}}`),
			wantStatus: metav1.ConditionUnknown,
			wantReason: konveyoriov1alpha1.AgentRunReasonVerificationNotReported,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := &konveyoriov1alpha1.AgentRun{}
			run.Generation = 3
			setRunVerified(run, tc.td)

			cond := meta.FindStatusCondition(run.Status.Conditions,
				konveyoriov1alpha1.AgentRunConditionVerified)
			if cond == nil {
				t.Fatalf("Verified condition not set")
			}
			if cond.Status != tc.wantStatus {
				t.Errorf("Verified.Status = %q, want %q", cond.Status, tc.wantStatus)
			}
			if cond.Reason != tc.wantReason {
				t.Errorf("Verified.Reason = %q, want %q", cond.Reason, tc.wantReason)
			}
			if cond.Message == "" {
				t.Errorf("Verified.Message is empty; every verdict must say why")
			}
			if tc.wantInMessage != "" && !strings.Contains(cond.Message, tc.wantInMessage) {
				t.Errorf("Verified.Message = %q, want it to contain %q", cond.Message, tc.wantInMessage)
			}
			if cond.ObservedGeneration != 3 {
				t.Errorf("Verified.ObservedGeneration = %d, want 3", cond.ObservedGeneration)
			}
		})
	}
}

// Verified=True is the single claim consumers key off, so it must be
// reachable from exactly one input: an explicit "passed".
func TestVerifiedTrueOnlyFromExplicitPass(t *testing.T) {
	notPasses := []string{
		`{}`,
		`{"verification":{}}`,
		`{"verification":{"status":""}}`,
		`{"verification":{"status":"failed"}}`,
		`{"verification":{"status":"notRun"}}`,
		`{"verification":{"status":"skipped"}}`,
		`{"verification":{"status":"PASSED"}}`,
		`{"verification":{"status":"passed "}}`,
		`{"verification":"passed"}`,
		`{"verification":{"commands":[{"label":"Build","status":"passed","exitCode":0}]}}`,
		`not json at all`,
	}
	for _, raw := range notPasses {
		status, reason, _ := verificationVerdict(blob(raw))
		if status == metav1.ConditionTrue {
			t.Errorf("%s yielded Verified=True (reason %s); only an explicit "+
				`"passed" may claim a verified tree`, raw, reason)
		}
	}
	if status, _, _ := verificationVerdict(nil); status == metav1.ConditionTrue {
		t.Errorf("absent terminationData yielded Verified=True")
	}
}

// The condition carries the verdict only. The per-command evidence stays
// in terminationData, and the message says so, so a consumer knows where
// to drill down (#302, konveyor/tackle2-ui#3703).
func TestVerifiedMessagePointsAtEvidence(t *testing.T) {
	for _, raw := range []string{
		`{"verification":{"status":"passed"}}`,
		`{"verification":{"status":"failed","reason":"Build failed"}}`,
		`{"verification":{"status":"notRun","reason":"HARNESS_VERIFY=off"}}`,
	} {
		_, _, message := verificationVerdict(blob(raw))
		if !strings.Contains(message, "status.terminationData.verification.commands") {
			t.Errorf("message for %s does not point at the evidence: %q", raw, message)
		}
	}
}

// A malformed blob must not inflate the status object with an unbounded
// reason string.
func TestVerifiedMessageIsBounded(t *testing.T) {
	long := strings.Repeat("x", verificationVerdictMaxRunes*3)
	_, _, message := verificationVerdict(blob(
		`{"verification":{"status":"failed","reason":"` + long + `"}}`))
	if len(message) > len(evidencePointer)+verificationVerdictMaxRunes+256 {
		t.Errorf("message is %d bytes; the harness reason should be clipped", len(message))
	}
	if !strings.Contains(message, "…") {
		t.Errorf("clipped message should be marked as clipped: %q", message)
	}
}

// Re-reconciling a terminal run must not churn lastTransitionTime.
func TestSetRunVerifiedIsIdempotent(t *testing.T) {
	run := &konveyoriov1alpha1.AgentRun{}
	td := blob(`{"verification":{"status":"failed","reason":"Build failed"}}`)

	setRunVerified(run, td)
	first := meta.FindStatusCondition(run.Status.Conditions,
		konveyoriov1alpha1.AgentRunConditionVerified).DeepCopy()

	setRunVerified(run, td)
	second := meta.FindStatusCondition(run.Status.Conditions,
		konveyoriov1alpha1.AgentRunConditionVerified)

	if len(run.Status.Conditions) != 1 {
		t.Fatalf("got %d conditions, want 1", len(run.Status.Conditions))
	}
	if !second.LastTransitionTime.Equal(&first.LastTransitionTime) {
		t.Errorf("lastTransitionTime moved on an unchanged verdict")
	}
}

// Verification is orthogonal to the run's outcome: it must leave phase
// and the Succeeded condition exactly as setTerminalOutcome left them.
// A Succeeded=True run with Verified=False is the whole point of #302.
func TestVerifiedDoesNotChangeOutcome(t *testing.T) {
	r := &AgentRunReconciler{}
	run := &konveyoriov1alpha1.AgentRun{}
	r.setTerminalOutcome(run, podWithExit(0, ""), sandboxFinishedReasonSucceeded, "")
	setRunVerified(run, blob(`{"verification":{"status":"failed","reason":"Build failed"}}`))

	if run.Status.Phase != konveyoriov1alpha1.AgentRunPhaseSucceeded {
		t.Errorf("phase = %q, want %q — verification must not move the phase",
			run.Status.Phase, konveyoriov1alpha1.AgentRunPhaseSucceeded)
	}
	succeeded := meta.FindStatusCondition(run.Status.Conditions,
		konveyoriov1alpha1.AgentRunConditionSucceeded)
	if succeeded == nil || succeeded.Status != metav1.ConditionTrue ||
		succeeded.Reason != konveyoriov1alpha1.AgentRunReasonSucceeded {
		t.Errorf("Succeeded condition = %+v, want True/Succeeded untouched", succeeded)
	}
	verified := meta.FindStatusCondition(run.Status.Conditions,
		konveyoriov1alpha1.AgentRunConditionVerified)
	if verified == nil || verified.Status != metav1.ConditionFalse {
		t.Fatalf("Verified condition = %+v, want False", verified)
	}
}
