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
	// harnessVerificationIncomplete — the harness had something to
	// measure and could not finish measuring it: the run was cancelled
	// part way, the step cap left commands unrun, or the working tree
	// was not the commit being pushed. Added to the harness's enum after
	// #302 was written; it is a definite non-pass, which is why it maps
	// to False rather than sharing notRun's "nothing to measure" reason.
	harnessVerificationIncomplete = "incomplete"
)

// verificationVerdictMaxRunes bounds how much of the harness's own reason
// string is copied into the condition message. The harness already bounds
// it, so this only guards against a malformed blob inflating the status.
const verificationVerdictMaxRunes = 512

// verificationCommitRunes is how much of the verified commit is shown in
// the condition message — the same abbreviation the harness uses in its
// own logs. The full SHA stays in
// status.terminationData.verification.commit.
const verificationCommitRunes = 12

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
	// Commit is the commit the harness's result describes: the tip of
	// the branch being pushed, which the working tree was confirmed to
	// match before any command ran (#300). Empty when nothing was
	// measured. Read only to name it in the condition message — a
	// verdict that does not say which tree it checked is not checkable.
	Commit string `json:"commit,omitempty"`
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
//	passed     → True    + VerificationPassed
//	failed     → False   + VerificationFailed
//	incomplete → False   + VerificationIncomplete
//	notRun     → False   + VerificationNotRun
//	nothing    → Unknown + VerificationNotReported
//
// Where the harness names the commit it measured, the condition message
// names it too: the verdict is only checkable against a tree.
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
			withDetail("Every verification command in the run's plan ran and exited 0", v)
	case v.Status == harnessVerificationFailed:
		return metav1.ConditionFalse,
			konveyoriov1alpha1.AgentRunReasonVerificationFailed,
			withDetail("A verification command in the run's plan did not pass", v)
	case v.Status == harnessVerificationIncomplete:
		// The harness measured and could not finish — a different fact
		// from "nothing to measure" (notRun) and from "the controller
		// cannot say" (NotReported). False either way: a partial
		// measurement is not a verified tree.
		return metav1.ConditionFalse,
			konveyoriov1alpha1.AgentRunReasonVerificationIncomplete,
			withDetail("Verification of the run's plan could not be completed, "+
				"so the pushed tree is not verified", v)
	case v.Status == harnessVerificationNotRun:
		return metav1.ConditionFalse,
			konveyoriov1alpha1.AgentRunReasonVerificationNotRun,
			withDetail("No verification command ran, so the pushed tree is unverified", v)
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

// withDetail names the commit the harness measured, appends the
// harness's own reason, and points at where the per-command evidence
// lives. The commit is what makes a verdict checkable, so it goes in
// front of the reason; it is absent whenever the harness measured
// nothing (notRun, and the incomplete case where it could not identify
// the commit being pushed at all).
func withDetail(summary string, v *harnessVerification) string {
	var b strings.Builder
	b.WriteString(summary)
	if c := shortCommit(v.Commit); c != "" {
		b.WriteString(" on commit ")
		b.WriteString(c)
	}
	if r := strings.TrimSpace(v.Reason); r != "" {
		b.WriteString(": ")
		b.WriteString(truncateRunes(r, verificationVerdictMaxRunes))
	}
	b.WriteString(". ")
	b.WriteString(evidencePointer)
	return b.String()
}

// shortCommit abbreviates the harness's recorded commit for the condition
// message, and returns "" for anything that is not a commit. The blob is
// the harness's schema and a future or corrupt one could put anything
// under the key, so the message says "on commit <sha>" only for something
// that is one — a bad value is dropped rather than presented as a tree
// the run was verified against.
func shortCommit(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) < 7 || len(sha) > 64 {
		return ""
	}
	for _, r := range sha {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return ""
		}
	}
	return truncateRunesPlain(sha, verificationCommitRunes)
}

// truncateRunesPlain clips s to at most max runes without marking the cut:
// an abbreviated SHA is a conventional form, not a damaged string.
func truncateRunesPlain(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

// truncateRunes clips s to at most max runes, marking that it was cut.
func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}
