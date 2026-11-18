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

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

// Values of the termination blob's costCeiling key. The harness omits the
// key when the run had no maxCost to enforce.
const (
	harnessCostCeilingEnforced   = "enforced"
	harnessCostCeilingUnenforced = "unenforced"
)

// setRunCostCeiling derives the CostCeilingEnforced condition from the
// costCeiling key of the harness termination blob. A blob without a value
// this controller recognises sets no condition: there is no ceiling to
// report on, or nothing that can be said about one.
//
// The key is decoded on its own so that a malformed value cannot cost the
// run the verification verdict read from the same blob.
func setRunCostCeiling(run *konveyoriov1alpha1.AgentRun, td *runtime.RawExtension) {
	if td == nil || len(td.Raw) == 0 {
		return
	}
	var blob struct {
		CostCeiling string `json:"costCeiling"`
	}
	if err := json.Unmarshal(td.Raw, &blob); err != nil {
		return
	}

	condition := metav1.Condition{
		Type:               konveyoriov1alpha1.AgentRunConditionCostCeilingEnforced,
		ObservedGeneration: run.Generation,
	}
	switch blob.CostCeiling {
	case harnessCostCeilingEnforced:
		condition.Status = metav1.ConditionTrue
		condition.Reason = konveyoriov1alpha1.AgentRunReasonCostReported
		condition.Message = "The agent runtime reported this run's cost, so maxCost was enforced against it"
	case harnessCostCeilingUnenforced:
		condition.Status = metav1.ConditionFalse
		condition.Reason = konveyoriov1alpha1.AgentRunReasonCostNotReported
		condition.Message = "The agent runtime reported no cost for this run, so the configured maxCost " +
			"could not be enforced and did not limit its spend; this usually means the runtime has no price for the model"
	default:
		return
	}
	meta.SetStatusCondition(&run.Status.Conditions, condition)
}
