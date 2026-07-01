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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentPlaybookStage defines one stage in a playbook.
// Each stage references an Agent and carries instructions.
type AgentPlaybookStage struct {
	// Name is the stage name, unique within the playbook.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// AgentRef is the name of the Agent CR to execute for this stage.
	// +kubebuilder:validation:MinLength=1
	AgentRef string `json:"agentRef"`

	// Instructions are task-specific instructions for this stage.
	// Composed with the Agent's prompt at execution time.
	// +optional
	Instructions string `json:"instructions,omitempty"`
}

// AgentPlaybookSpec defines the desired state of an AgentPlaybook.
type AgentPlaybookSpec struct {
	// Guide is a high-level guide providing ambient context for all stages.
	// Written as a context file in the workspace so each agent understands
	// where its work fits in the bigger picture.
	// +optional
	Guide string `json:"guide,omitempty"`

	// Stages is the ordered sequence of stages. Each stage references an
	// Agent and carries instructions. Stages execute sequentially, sharing
	// the same target branch. Cross-stage continuity comes from committed
	// handoff files.
	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=name
	Stages []AgentPlaybookStage `json:"stages"`
}

// AgentPlaybookStatus defines the observed state of an AgentPlaybook.
type AgentPlaybookStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest available observations of the
	// AgentPlaybook's state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ap
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentPlaybook is a reusable playbook combining a high-level guide with an
// ordered sequence of stages. Each stage references an Agent and carries
// instructions. An AgentPlaybook is a template — creating one does not
// execute anything.
type AgentPlaybook struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentPlaybookSpec   `json:"spec,omitempty"`
	Status AgentPlaybookStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentPlaybookList contains a list of AgentPlaybook.
type AgentPlaybookList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentPlaybook `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentPlaybook{}, &AgentPlaybookList{})
}
