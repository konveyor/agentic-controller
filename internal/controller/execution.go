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

import konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"

// defaultExecutionMode is the supervision policy applied when a run,
// stage, and Agent all leave mode unset (ADR 0011).
const defaultExecutionMode = konveyoriov1alpha1.ExecutionModeAuto

// resolveExecution merges an override ExecutionSpec (from an AgentRun or
// a workflow stage) over the Agent's default ExecutionLimits, field by
// field: each field takes the override's value if set, else the Agent
// default. Mode comes only from the override — the Agent declares no mode
// (ADR 0011/0018). Either argument may be nil. Returns nil only when both
// inputs contribute nothing, so callers can leave Execution unset on the
// child.
//
// This is the single resolution rule for execution config (ADR 0018):
// the AgentWorkflowRun controller uses it for (stage, Agent) and the
// AgentRun controller uses it for (run, Agent). It does not apply the
// mode default — that is left to effectiveMode at delivery time so the
// stored value stays a faithful record of what was set.
func resolveExecution(
	override *konveyoriov1alpha1.ExecutionSpec,
	base *konveyoriov1alpha1.ExecutionLimits,
) *konveyoriov1alpha1.ExecutionSpec {
	if override == nil && base == nil {
		return nil
	}
	if override == nil {
		override = &konveyoriov1alpha1.ExecutionSpec{}
	}
	if base == nil {
		base = &konveyoriov1alpha1.ExecutionLimits{}
	}

	resolved := &konveyoriov1alpha1.ExecutionSpec{
		Mode: override.Mode,
		ExecutionLimits: konveyoriov1alpha1.ExecutionLimits{
			MaxTurns: override.MaxTurns,
			MaxCost:  override.MaxCost,
		},
	}
	if resolved.MaxTurns == nil {
		resolved.MaxTurns = base.MaxTurns
	}
	if resolved.MaxCost == "" {
		resolved.MaxCost = base.MaxCost
	}

	if resolved.Mode == "" && resolved.MaxTurns == nil && resolved.MaxCost == "" {
		return nil
	}
	return resolved
}

// effectiveMode returns the supervision mode to enforce for a resolved
// ExecutionSpec, applying the auto default when mode is unset.
func effectiveMode(exec *konveyoriov1alpha1.ExecutionSpec) konveyoriov1alpha1.ExecutionMode {
	if exec == nil || exec.Mode == "" {
		return defaultExecutionMode
	}
	return exec.Mode
}
