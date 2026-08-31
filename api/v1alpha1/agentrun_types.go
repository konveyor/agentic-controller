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
	"k8s.io/apimachinery/pkg/runtime"
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

	// AgentRunConditionSucceeded is the terminal outcome, following the
	// Knative/Tekton run-to-completion convention (ADR 0018): Unknown
	// while the run is in progress, True on clean completion, and False
	// with a reason when the run did not cleanly complete. It is the
	// forward replacement for the Ready condition as the terminal signal;
	// Phase remains a coarse mirror during the transition.
	AgentRunConditionSucceeded = "Succeeded"
)

// AgentRun reasons on the Succeeded condition. Terminal reasons pair
// with Status True/False; in-progress reasons pair with Status Unknown
// (ADR 0018). Progress states that are self-evident from other fields
// (waiting for the Agent, sandbox creation) set their own reason strings
// at the call site.
const (
	// AgentRunReasonSucceeded — clean completion (harness exit 0).
	AgentRunReasonSucceeded = "Succeeded"
	// AgentRunReasonFailed — an error during execution (harness exit 1).
	AgentRunReasonFailed = "Failed"
	// AgentRunReasonLimitReached — an execution budget was exhausted and
	// the harness committed a handoff (harness exit 2, ADR 0011/0018).
	AgentRunReasonLimitReached = "LimitReached"
	// AgentRunReasonRunning — the agent process is executing; Succeeded
	// is Unknown until the run ends.
	AgentRunReasonRunning = "Running"
)

// ParamValue supplies a value for a declared Agent parameter.
type ParamValue struct {
	// Name is the parameter name, matching an Agent param declaration.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Value is the parameter value.
	Value string `json:"value"`
}

// FileMount attaches a Secret or ConfigMap into the Sandbox container as
// files on disk, mounted read-only. It is the file-shaped sibling of
// spec.envFrom: use it for config or credentials a skill or tool reads
// from a path (e.g. a config file, a service-account JSON) rather than
// from an environment variable. The controller passes the source through
// as an opaque Kubernetes primitive — it never reads or interprets the
// contents.
//
// Exactly one of SecretName or ConfigMapName must be set. MountPath is
// rejected at run creation if it lands on, under, or above a
// controller-managed mount (/opt/skills, /opt/skills-src, /run/konveyor,
// /workspace, /tmp).
// +kubebuilder:validation:XValidation:rule="has(self.secretName) != has(self.configMapName)",message="exactly one of secretName or configMapName must be set"
type FileMount struct {
	// SecretName names a Secret in the same namespace to mount. Mutually
	// exclusive with ConfigMapName.
	// +optional
	// +kubebuilder:validation:MinLength=1
	SecretName string `json:"secretName,omitempty"`

	// ConfigMapName names a ConfigMap in the same namespace to mount.
	// Mutually exclusive with SecretName.
	// +optional
	// +kubebuilder:validation:MinLength=1
	ConfigMapName string `json:"configMapName,omitempty"`

	// MountPath is the absolute path in the Sandbox container where the
	// content is mounted. When SubPath is empty the whole object is
	// mounted as a directory here (one file per key); when SubPath is set
	// MountPath is the file path a single key lands at. Must not collide
	// with a controller-managed mount.
	// +kubebuilder:validation:MinLength=1
	MountPath string `json:"mountPath"`

	// SubPath mounts a single key from the source object as a file at
	// MountPath instead of mounting the whole object as a directory. Its
	// value is the key name within the Secret or ConfigMap.
	// +optional
	SubPath string `json:"subPath,omitempty"`

	// Items selects and optionally renames individual keys from the
	// source object (directory mount). When empty, every key is projected
	// under MountPath using the key as the filename.
	// +optional
	Items []corev1.KeyToPath `json:"items,omitempty"`
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
	// Resolved values are written to /run/konveyor/params.json in the
	// Sandbox (see ADR 0009); they are not injected as env vars.
	// +optional
	// +listType=map
	// +listMapKey=name
	Params []ParamValue `json:"params,omitempty"`

	// Instructions are task-specific instructions for this run.
	// Composed with the Agent's prompt at execution time. Supports
	// $(agent.<name>) / $(workflow.<name>) substitution.
	// +optional
	Instructions string `json:"instructions,omitempty"`

	// Execution carries the resolved supervision mode and budget limits
	// for this run. For a standalone run these are set by the run
	// creator (mode) and default from the Agent (limits). For a workflow
	// stage the AgentWorkflowRun controller stamps the stage-resolved
	// values here (ADR 0018). Unset fields resolve against the Agent's
	// Execution defaults; mode resolves to auto when unset everywhere.
	// +optional
	Execution *ExecutionSpec `json:"execution,omitempty"`

	// WorkflowParams carries the resolved, type-coerced workflow-level
	// parameters for a stage run, as a JSON object stamped by the
	// AgentWorkflowRun controller (ADR 0018). The AgentRun controller
	// writes it verbatim to the "workflow" section of params.json and
	// uses its values for $(workflow.<name>) substitution. Empty for
	// standalone runs. Coercion happens in the workflow controller,
	// which owns the AgentWorkflow param declarations.
	// +optional
	WorkflowParams *runtime.RawExtension `json:"workflowParams,omitempty"`

	// Env is a list of additional environment variables to set in the
	// Sandbox container. Passed through to the Sandbox unchanged.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// EnvFrom is a list of sources to populate environment variables in
	// the Sandbox container. Passed through to the Sandbox unchanged.
	// +optional
	EnvFrom []corev1.EnvFromSource `json:"envFrom,omitempty"`

	// FileMounts attaches Secrets or ConfigMaps into the Sandbox
	// container as read-only files, for config or credentials a skill or
	// tool reads from disk rather than from the environment. Each
	// mountPath is validated against the controller-managed mounts and the
	// run fails terminally (InvalidFileMounts) if any collides.
	// +optional
	// +listType=map
	// +listMapKey=mountPath
	FileMounts []FileMount `json:"fileMounts,omitempty"`

	// GitConfig overrides the Agent's git commit identity for this run.
	// When set it replaces the Agent's GitConfig wholesale (name and email
	// together); when unset the run uses the Agent's identity, then the
	// harness default.
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
	// connections (see AgentRunConditionACPReady). Succeeded is the
	// terminal outcome (see AgentRunConditionSucceeded): Unknown while
	// running, True on clean completion, False with a reason
	// (Failed, LimitReached) otherwise.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// TerminationData is the raw JSON the harness wrote to the pod's
	// termination message (/dev/termination-log) on exit — typically a
	// usage/cost report. The controller copies it verbatim and never
	// interprets it; platform-specific UIs read the harness's schema
	// (ADR 0011/0018). Absent if the harness wrote nothing parseable.
	// +optional
	TerminationData *runtime.RawExtension `json:"terminationData,omitempty"`
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
// key-value parameters (delivered via /run/konveyor/params.json and
// referenced in prompt text as $(agent.<name>) / $(workflow.<name>) — ADR
// 0009). The controller validates, resolves skills to ImageVolumes, creates
// a Sandbox, and tracks status to completion.
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
