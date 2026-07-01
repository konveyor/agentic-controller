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

// AgentRunPhase represents the phase of an AgentRun.
// +kubebuilder:validation:Enum=Pending;Running;Succeeded;Failed
type AgentRunPhase string

const (
	AgentRunPhasePending   AgentRunPhase = "Pending"
	AgentRunPhaseRunning   AgentRunPhase = "Running"
	AgentRunPhaseSucceeded AgentRunPhase = "Succeeded"
	AgentRunPhaseFailed    AgentRunPhase = "Failed"
)

// AgentRunModelSelection selects a specific provider and model for this run.
type AgentRunModelSelection struct {
	// Role is the purpose of this model in the run (e.g. "primary", "efficient").
	// The harness maps roles to runtime-specific configuration.
	// +kubebuilder:validation:MinLength=1
	Role string `json:"role"`

	// Provider is the name of an LLMProvider CR. Must be in the Agent's
	// providers list.
	// +kubebuilder:validation:MinLength=1
	Provider string `json:"provider"`

	// Model is the model identifier. Must be declared on the referenced
	// LLMProvider.
	// +kubebuilder:validation:MinLength=1
	Model string `json:"model"`
}

// AgentRunParam supplies a value for a declared Agent parameter.
type AgentRunParam struct {
	// Name is the parameter name, matching an Agent param declaration.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Value is the parameter value.
	Value string `json:"value"`
}

// AgentRunSpec defines the desired state of an AgentRun.
// +kubebuilder:validation:XValidation:rule="self.agentRef == oldSelf.agentRef",message="agentRef is immutable"
type AgentRunSpec struct {
	// AgentRef is the name of the Agent CR to execute.
	// +kubebuilder:validation:MinLength=1
	AgentRef string `json:"agentRef"`

	// Models selects specific provider/model combinations for this run.
	// Each entry maps a role to a provider and model from the Agent's
	// available set.
	// +optional
	// +listType=map
	// +listMapKey=role
	Models []AgentRunModelSelection `json:"models,omitempty"`

	// Params supplies values for the Agent's declared parameters.
	// Injected as KONVEYOR_PARAM_{NAME} env vars into the Sandbox.
	// +optional
	// +listType=map
	// +listMapKey=name
	Params []AgentRunParam `json:"params,omitempty"`

	// Instructions are task-specific instructions for this run.
	// Composed with the Agent's prompt at execution time.
	// +optional
	Instructions string `json:"instructions,omitempty"`

	// Env is a list of additional environment variables to set in the
	// Sandbox container. Passed through to the Sandbox unchanged.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// EnvFrom is a list of sources to populate environment variables in
	// the Sandbox container. Passed through to the Sandbox unchanged.
	// +optional
	EnvFrom []corev1.EnvFromSource `json:"envFrom,omitempty"`
}

// AgentRunStatus defines the observed state of an AgentRun.
type AgentRunStatus struct {
	// Phase is the current phase of the AgentRun.
	// +kubebuilder:default=Pending
	// +optional
	Phase AgentRunPhase `json:"phase,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// SandboxName is the name of the Sandbox CR created for this run.
	// +optional
	SandboxName string `json:"sandboxName,omitempty"`

	// StartTime is the time the Sandbox started running.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// CompletionTime is the time the Sandbox finished.
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// Duration is the wall-clock duration of the run in seconds.
	// +optional
	Duration *int64 `json:"duration,omitempty"`

	// SecretKeyRef references a Secret containing the ACP authentication key
	// for connecting to the agent's ACP endpoint. The harness generates
	// a random key per run and stores it in this Secret.
	// +optional
	SecretKeyRef *corev1.LocalObjectReference `json:"secretKeyRef,omitempty"`

	// Conditions represent the latest available observations of the
	// AgentRun's state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ar
// +kubebuilder:printcolumn:name="Agent",type=string,JSONPath=`.spec.agentRef`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:printcolumn:name="Duration",type=integer,JSONPath=`.status.duration`,priority=1

// AgentRun is a request to execute a single Agent with specific selections.
// It references an Agent, selects providers and models, carries instructions
// and key-value parameters (injected as env vars into the Sandbox). The
// controller validates, resolves skills to ImageVolumes, creates a Sandbox,
// and tracks status to completion.
type AgentRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentRunSpec   `json:"spec,omitempty"`
	Status AgentRunStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentRunList contains a list of AgentRun.
type AgentRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentRun `json:"items"`
}
