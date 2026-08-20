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

// ParamType defines the type of a parameter.
// +kubebuilder:validation:Enum=string;number;boolean
type ParamType string

const (
	ParamTypeString  ParamType = "string"
	ParamTypeNumber  ParamType = "number"
	ParamTypeBoolean ParamType = "boolean"
)

// Param declares a typed parameter that an Agent or AgentWorkflow
// accepts. A run (AgentRun / AgentWorkflowRun) supplies values via
// ParamValue. Resolved values are delivered to the Sandbox in
// /run/konveyor/params.json (see ADR 0009) and may be referenced in
// prompt text with $(agent.<name>) / $(workflow.<name>).
// +kubebuilder:validation:XValidation:rule="!(has(self.required) && self.required && has(self.default) && size(self.default) > 0)",message="a parameter with a default value cannot be required"
type Param struct {
	// Name is the parameter name. Referenced in prompt text as
	// $(agent.<name>) or $(workflow.<name>) and delivered in
	// params.json under the corresponding section.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-zA-Z_][a-zA-Z0-9_]*$`
	Name string `json:"name"`

	// Type is the parameter type. Controls JSON coercion in
	// params.json: number as a JSON number, boolean as a JSON boolean,
	// string as a JSON string.
	// +kubebuilder:default=string
	// +optional
	Type ParamType `json:"type,omitempty"`

	// Description explains the purpose of the parameter.
	// +optional
	Description string `json:"description,omitempty"`

	// Default is the default value if not supplied by a run.
	// +optional
	Default string `json:"default,omitempty"`

	// Required indicates whether the parameter must be supplied.
	// A parameter with a default is never required.
	// +optional
	Required bool `json:"required,omitempty"`
}

// ExecutionMode controls the supervision policy for a run's tool calls.
// +kubebuilder:validation:Enum=auto;approve
type ExecutionMode string

const (
	// ExecutionModeAuto approves all tool calls automatically. Headless-safe.
	ExecutionModeAuto ExecutionMode = "auto"
	// ExecutionModeApprove requires explicit approval for tool calls,
	// relayed to attached viewers by the harness tee (ADR 0008). With no
	// viewer attached, the fail-closed policy denies all tool calls.
	ExecutionModeApprove ExecutionMode = "approve"
)

// ExecutionLimits are the budget ceilings for a run: whichever is hit
// first triggers wind-down (ADR 0011). These are template-level concerns
// the Agent author owns, so they are declared on the Agent as defaults
// and may be overridden per AgentWorkflow stage. They deliberately do
// NOT include mode — mode is an execution-time concern, not a template
// concern (ADR 0011/0018). Who may set or raise limits is a UI/API (Hub)
// governance concern, not enforced by the controller (ADR 0018).
type ExecutionLimits struct {
	// MaxTurns is the maximum number of turns before wind-down. The unit
	// is runtime-defined (e.g. Goose counts turns without user input).
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxTurns *int `json:"maxTurns,omitempty"`

	// MaxCost is the maximum cumulative cost in USD before wind-down,
	// as a decimal string (e.g. "10.00"). USD only: the enforcement
	// threshold must match the currency of reported usage, which is USD
	// across the supported runtimes. Reported cost may carry an ISO 4217
	// currency; this threshold does not.
	// +kubebuilder:validation:Pattern=`^[0-9]+(\.[0-9]{1,2})?$`
	// +optional
	MaxCost string `json:"maxCost,omitempty"`
}

// ExecutionSpec is the full per-invocation execution config: supervision
// mode plus the budget limits. It is set on AgentRun (standalone runs)
// and on AgentWorkflow stages (per-stage), never on the Agent — the
// Agent carries only ExecutionLimits, because mode is an execution-time
// concern (ADR 0011/0018). The AgentRun carries the resolved values,
// written to the execution section of params.json. Fields are
// individually optional so a run or stage can override one without
// restating the others; resolution is per-field (run/stage value if set,
// else Agent default for the limits).
type ExecutionSpec struct {
	// Mode is the supervision policy for tool calls. Defaults to auto.
	// +optional
	Mode ExecutionMode `json:"mode,omitempty"`

	// ExecutionLimits are the budget ceilings (maxTurns, maxCost),
	// resolved against the Agent's defaults.
	ExecutionLimits `json:",inline"`
}

// AgentGatewayRef references a Gateway by name.
type AgentGatewayRef struct {
	// Ref is the name of a Gateway CR in the same namespace.
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

	// Gateways is the set of gateways (provider/model combinations)
	// available for runs. At least one gateway must be specified.
	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=ref
	Gateways []AgentGatewayRef `json:"gateways"`

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
	Params []Param `json:"params,omitempty"`

	// Execution declares the default budget limits (maxTurns, maxCost)
	// for runs of this Agent. Runs and workflow stages may override
	// these; the AgentRun carries the resolved values (ADR 0018). The
	// Agent does not declare mode — mode is an execution-time concern set
	// on the AgentRun or stage (ADR 0011/0018).
	// +optional
	Execution *ExecutionLimits `json:"execution,omitempty"`
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
// It references SkillCards, SkillCollections, Gateways, a container image,
// a prompt, and typed parameters. An Agent does not select a specific model —
// gateway selection happens at execution time via AgentRun.
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
