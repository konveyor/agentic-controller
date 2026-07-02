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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentPlaybookRunStageStatus tracks the status of a single stage within
// a playbook run.
type AgentPlaybookRunStageStatus struct {
	// Name is the stage name, matching a stage in the AgentPlaybook.
	Name string `json:"name"`

	// Phase is the current phase of this stage.
	Phase AgentRunPhase `json:"phase"`

	// AgentRunName is the name of the AgentRun CR created for this stage.
	// +optional
	AgentRunName string `json:"agentRunName,omitempty"`
}

// AgentPlaybookRunSpec defines the desired state of an AgentPlaybookRun.
// The spec is immutable once created — delete and recreate to change values.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
type AgentPlaybookRunSpec struct {
	// PlaybookRef is the name of the AgentPlaybook CR to execute.
	// +kubebuilder:validation:MinLength=1
	PlaybookRef string `json:"playbookRef"`

	// Models selects specific provider/model combinations for all stages.
	// Individual stages may override these selections in the future.
	// +optional
	// +listType=map
	// +listMapKey=role
	Models []AgentRunModelSelection `json:"models,omitempty"`

	// Params supplies values for Agent parameters across all stages.
	// +optional
	// +listType=map
	// +listMapKey=name
	Params []AgentRunParam `json:"params,omitempty"`

	// Env is a list of additional environment variables to set across
	// all stages. Passed through to each AgentRun's Sandbox unchanged.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// EnvFrom is a list of sources to populate environment variables
	// across all stages. Passed through to each AgentRun's Sandbox.
	// +optional
	EnvFrom []corev1.EnvFromSource `json:"envFrom,omitempty"`
}

// AgentPlaybookRunStatus defines the observed state of an AgentPlaybookRun.
type AgentPlaybookRunStatus struct {
	// Phase is the current phase of the overall playbook run.
	// +kubebuilder:default=Pending
	// +optional
	Phase AgentRunPhase `json:"phase,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// CurrentStage is the name of the currently executing stage.
	// +optional
	CurrentStage string `json:"currentStage,omitempty"`

	// Stages tracks the status of each stage.
	// +optional
	// +listType=map
	// +listMapKey=name
	Stages []AgentPlaybookRunStageStatus `json:"stages,omitempty"`

	// StartTime is the time the playbook run started.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// CompletionTime is the time the playbook run finished.
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// Conditions represent the latest available observations of the
	// AgentPlaybookRun's state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=apr
// +kubebuilder:printcolumn:name="Playbook",type=string,JSONPath=`.spec.playbookRef`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Current Stage",type=string,JSONPath=`.status.currentStage`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentPlaybookRun is a request to execute an AgentPlaybook. It references
// an AgentPlaybook and carries generic parameters. The controller orchestrates
// execution: creates an AgentRun per stage, manages cross-stage handoff via
// committed files on a shared target branch.
type AgentPlaybookRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentPlaybookRunSpec   `json:"spec,omitempty"`
	Status AgentPlaybookRunStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentPlaybookRunList contains a list of AgentPlaybookRun.
type AgentPlaybookRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentPlaybookRun `json:"items"`
}
