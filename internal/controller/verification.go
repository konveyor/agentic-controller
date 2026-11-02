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
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

// Harness verification contract (#300). The harness re-runs the commands
// in its plan's "## Verification" section after the agent's turn and
// before the push, and records the result under the "verification" key of
// the termination blob. These are the only values of that object's
// "status" the controller recognises; anything else is treated as no
// verdict at all (see setRunVerified).
const (
	harnessVerificationPassed = "passed"
	harnessVerificationFailed = "failed"
	harnessVerificationNotRun = "notRun"
)

// verificationVerdictMaxRunes bounds how much of the harness's own reason
// string is copied into the condition message. The harness already bounds
// it, so this only guards against a malformed blob inflating the status.
const verificationVerdictMaxRunes = 512

// evidencePointer tells a reader where the per-command detail lives. The
// condition carries the verdict only; the controller does not lift the
// commands, exit codes or output tails onto the API (ADR 0011/0018).
const evidencePointer = "status.terminationData.verification.commands records each command and its exit code"

// harnessTermination is the narrow view the controller takes of the
// harness's termination blob. Everything else in it — usage, cost,
// stopReason, handoffStatus, the per-command verification evidence —
// stays opaque and is served to clients verbatim from
// status.terminationData.
type harnessTermination struct {
	Verification *harnessVerification `json:"verification,omitempty"`
}

// harnessVerification is the verdict half of the harness's verification
// record. The commands array is deliberately not modelled.
type harnessVerification struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// setRunVerified derives the AgentRun's Verified condition (#302) from the
// single "verification.status" key of the harness termination blob.
//
// This is the whole of the controller's interpretation of terminationData.
// It reads one closed enum and maps it onto the status vocabulary; the
// evidence behind the verdict stays in the blob, uninterpreted. The
// mapping is deliberately asymmetric — True is reachable only from an
// explicit "passed", so a missing, unreadable, trimmed or unrecognised
// record can never render as a verified pass:
//
//	passed  → True    + VerificationPassed
//	failed  → False   + VerificationFailed
//	notRun  → False   + VerificationNotRun
//	nothing → Unknown + VerificationNotReported
//
// Called once a run terminates, so every run that got far enough for the
// agent container to exit carries a Verified condition. Runs that never
// started (image pull, startup deadline) carry none: their Succeeded
// condition already says the agent never ran.
func setRunVerified(run *konveyoriov1alpha1.AgentRun, td *runtime.RawExtension) {
	status, reason, message := verificationVerdict(td)
	meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{
		Type:               konveyoriov1alpha1.AgentRunConditionVerified,
		Status:             status,
		ObservedGeneration: run.Generation,
		Reason:             reason,
		Message:            message,
	})
}

// verificationVerdict maps the termination blob to the Verified
// condition's status, reason and message. Split out from setRunVerified
// as a pure function to keep the mapping directly testable.
func verificationVerdict(td *runtime.RawExtension) (metav1.ConditionStatus, string, string) {
	v := harnessVerificationFrom(td)
	switch {
	case v == nil:
		return metav1.ConditionUnknown,
			konveyoriov1alpha1.AgentRunReasonVerificationNotReported,
			"The harness reported no verification result; this run's pushed tree was not independently checked"
	case v.Status == harnessVerificationPassed:
		return metav1.ConditionTrue,
			konveyoriov1alpha1.AgentRunReasonVerificationPassed,
			withDetail("Every verification command in the run's plan ran and exited 0", v.Reason)
	case v.Status == harnessVerificationFailed:
		return metav1.ConditionFalse,
			konveyoriov1alpha1.AgentRunReasonVerificationFailed,
			withDetail("A verification command in the run's plan did not pass", v.Reason)
	case v.Status == harnessVerificationNotRun:
		return metav1.ConditionFalse,
			konveyoriov1alpha1.AgentRunReasonVerificationNotRun,
			withDetail("No verification command ran, so the pushed tree is unverified", v.Reason)
	default:
		// A status this controller does not know. Newer harness, or a
		// corrupt blob; either way it is not a pass we can vouch for.
		return metav1.ConditionUnknown,
			konveyoriov1alpha1.AgentRunReasonVerificationNotReported,
			fmt.Sprintf("The harness reported an unrecognised verification status %q; "+
				"this run's pushed tree cannot be treated as verified",
				truncateRunes(v.Status, verificationVerdictMaxRunes))
	}
}

// harnessVerificationFrom extracts the verification verdict from the
// termination blob, or nil when there is none to read — no blob, a blob
// that is not a JSON object (so the harness's schema is not what is
// there), no verification key, or a verification object with an empty
// status. Unmarshalling errors are swallowed: terminationData is the
// harness's contract, and the controller declining to understand a
// version of it must leave the run reported rather than erroring.
func harnessVerificationFrom(td *runtime.RawExtension) *harnessVerification {
	if td == nil || len(td.Raw) == 0 {
		return nil
	}
	var blob harnessTermination
	if err := json.Unmarshal(td.Raw, &blob); err != nil {
		return nil
	}
	if blob.Verification == nil || blob.Verification.Status == "" {
		return nil
	}
	return blob.Verification
}

// withDetail appends the harness's own reason to the condition message,
// and points at where the per-command evidence lives.
func withDetail(summary, reason string) string {
	var b strings.Builder
	b.WriteString(summary)
	if r := strings.TrimSpace(reason); r != "" {
		b.WriteString(": ")
		b.WriteString(truncateRunes(r, verificationVerdictMaxRunes))
	}
	b.WriteString(". ")
	b.WriteString(evidencePointer)
	return b.String()
}

// truncateRunes clips s to at most max runes, marking that it was cut.
func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}
