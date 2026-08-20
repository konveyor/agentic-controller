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

// AgentRun condition types (status.conditions[].type).
const (
	// AgentRunConditionACPReady is True once the agent's ACP endpoint
	// accepts connections: the sandbox pod passes its tcpSocket:4000
	// readiness probe and the sandbox Service exists. It is the signal to
	// dial <sandboxName>.<namespace>.svc:4000 on — not Phase, which only
	// says the agent process is executing. Reasons: Listening,
	// NotListening, Finished.
	AgentRunConditionACPReady = "ACPReady"
)

// AgentRunParam supplies a value for a declared Agent parameter.
type AgentRunParam struct {
	// Name is the parameter name, matching an Agent param declaration.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Value is the parameter value.
	Value string `json:"value"`
}

// AgentRunSpec defines the desired state of an AgentRun.
// The spec is immutable once created — delete and recreate to change values.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
type AgentRunSpec struct {
	// AgentRef is the name of the Agent CR to execute.
	// +kubebuilder:validation:MinLength=1
	AgentRef string `json:"agentRef"`

	// Gateway selects the Gateway (provider/model combination) for this
	// run. Must be one of the gateways declared on the referenced Agent.
	// +optional
	// +kubebuilder:validation:MinLength=1
	Gateway string `json:"gateway,omitempty"`

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

	// GitConfig overrides the Agent's git commit identity for this run.
	// Fields left unset fall back to the Agent's GitConfig, then to the
	// harness default identity.
	// +optional
	GitConfig *GitConfig `json:"gitConfig,omitempty"`
}

// AgentRunStatus defines the observed state of an AgentRun.
type AgentRunStatus struct {
	// Phase is the current phase of the AgentRun. Running means the sandbox
	// pod is running (the agent process is executing); it says nothing about
	// whether the agent's ACP endpoint accepts connections yet — that is the
	// ACPReady condition. A run whose pod finishes before the controller
	// observes it running may go straight from Pending to a terminal phase.
	// +kubebuilder:default=Pending
	// +optional
	Phase AgentRunPhase `json:"phase,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// SandboxName is the name of the Sandbox CR created for this run.
	// +optional
	SandboxName string `json:"sandboxName,omitempty"`

	// StartTime is the time the sandbox pod started running (the Sandbox
	// creation time if the pod finished before the controller saw it run).
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
	// AgentRun's state. Ready tracks the run's overall outcome (False
	// while in progress with the current step as its reason, True on
	// success); ACPReady tracks whether the agent's ACP endpoint accepts
	// connections (see AgentRunConditionACPReady).
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
// It references an Agent, selects a gateway, carries instructions and
// key-value parameters (injected as env vars into the Sandbox). The
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
