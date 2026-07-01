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

// AgentParamType defines the type of a parameter.
// +kubebuilder:validation:Enum=string;number;boolean
type AgentParamType string

const (
	AgentParamTypeString  AgentParamType = "string"
	AgentParamTypeNumber  AgentParamType = "number"
	AgentParamTypeBoolean AgentParamType = "boolean"
)

// AgentParam declares a typed parameter that an Agent accepts.
// AgentRun supplies values for these parameters, which are injected as
// KONVEYOR_PARAM_{NAME} env vars into the Sandbox.
// +kubebuilder:validation:XValidation:rule="!(self.required && has(self.default) && self.default != ”)",message="a parameter with a default value cannot be required"
type AgentParam struct {
	// Name is the parameter name. Will be uppercased and prefixed with
	// KONVEYOR_PARAM_ when injected as an env var.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-zA-Z_][a-zA-Z0-9_]*$`
	Name string `json:"name"`

	// Type is the parameter type.
	// +kubebuilder:default=string
	// +optional
	Type AgentParamType `json:"type,omitempty"`

	// Description explains the purpose of the parameter.
	// +optional
	Description string `json:"description,omitempty"`

	// Default is the default value if not specified in AgentRun.
	// +optional
	Default string `json:"default,omitempty"`

	// Required indicates whether the parameter must be supplied.
	// A parameter with a default is never required.
	// +optional
	Required bool `json:"required,omitempty"`
}

// AgentProviderRef references an LLMProvider by name.
type AgentProviderRef struct {
	// Ref is the name of an LLMProvider CR in the same namespace.
	// +kubebuilder:validation:MinLength=1
	Ref string `json:"ref"`
}

// AgentSkillCardRef references a SkillCard by name.
type AgentSkillCardRef struct {
	// Ref is the name of a SkillCard CR in the same namespace.
	// +kubebuilder:validation:MinLength=1
	Ref string `json:"ref"`
}

// AgentSkillCollectionRef references a SkillCollection by name.
type AgentSkillCollectionRef struct {
	// Ref is the name of a SkillCollection CR in the same namespace.
	// +kubebuilder:validation:MinLength=1
	Ref string `json:"ref"`
}

// AgentSpec defines the desired state of an Agent.
type AgentSpec struct {
	// Image is the container image carrying the agent runtime and toolchains.
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// Prompt is the standing instructions for how the agent operates.
	// Composed with AgentRun instructions at execution time.
	// +optional
	Prompt string `json:"prompt,omitempty"`

	// Providers is the set of LLM providers and models available for runs.
	// At least one provider must be specified.
	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=ref
	Providers []AgentProviderRef `json:"providers"`

	// SkillCards references individual SkillCard CRs.
	// +optional
	// +listType=map
	// +listMapKey=ref
	SkillCards []AgentSkillCardRef `json:"skillCards,omitempty"`

	// SkillCollections references SkillCollection CRs.
	// +optional
	// +listType=map
	// +listMapKey=ref
	SkillCollections []AgentSkillCollectionRef `json:"skillCollections,omitempty"`

	// Params declares the typed parameters this Agent accepts.
	// +optional
	// +listType=map
	// +listMapKey=name
	Params []AgentParam `json:"params,omitempty"`
}

// AgentStatus defines the observed state of an Agent.
type AgentStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest available observations of the
	// Agent's state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ag
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Agent is a capability definition declaring what is available for execution.
// It references SkillCards, SkillCollections, LLMProviders, a container image,
// a prompt, and typed parameters. An Agent does not select a specific model —
// model selection happens at execution time via AgentRun.
type Agent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentSpec   `json:"spec,omitempty"`
	Status AgentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentList contains a list of Agent.
type AgentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Agent `json:"items"`
}
