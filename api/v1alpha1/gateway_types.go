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

// GatewayCredentialRef references a Secret containing API credentials.
type GatewayCredentialRef struct {
	// SecretName is the name of the Secret in the same namespace.
	// +kubebuilder:validation:MinLength=1
	SecretName string `json:"secretName"`

	// Key is the key within the Secret that contains the credential value.
	// When set, the credential is a single value (e.g. a bearer token) and
	// is injected into sandbox containers as KONVEYOR_LLM_API_KEY.
	// When omitted, the credential spans multiple environment variables
	// (e.g. AWS SigV4: AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY,
	// AWS_REGION) and the whole Secret is exposed to the sandbox container
	// via envFrom, with the Secret's keys as the variable names.
	// +kubebuilder:validation:MinLength=1
	// +optional
	Key string `json:"key,omitempty"`
}

// GatewayModel declares the model served by this gateway.
type GatewayModel struct {
	// Name is the model identifier (e.g. "claude-sonnet-4-20250514", "gpt-4o").
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// ContextWindow is the maximum context window size in tokens.
	// Used to validate that an Agent's always-loaded rules fit within budget.
	// +kubebuilder:validation:Minimum=1
	ContextWindow int64 `json:"contextWindow"`

	// Tier is an optional label for the model's capability tier
	// (e.g. "premium", "efficient").
	// +optional
	Tier string `json:"tier,omitempty"`
}

// GatewaySpec defines the desired state of a Gateway.
type GatewaySpec struct {
	// Provider is the runtime provider identifier (e.g. "anthropic",
	// "openai", "gcp-vertex-ai"). Injected as KONVEYOR_LLM_PROVIDER
	// so the harness can map credentials to provider-specific env vars.
	// This is a pre-OpenShell shim — when OpenShell is integrated,
	// inference.local eliminates the need for provider-specific
	// credential mapping and this field is removed.
	// +kubebuilder:validation:MinLength=1
	Provider string `json:"provider"`

	// Endpoint is the base URL of the LLM service.
	// +kubebuilder:validation:MinLength=1
	Endpoint string `json:"endpoint"`

	// CredentialRef references a Secret containing the API credential.
	CredentialRef GatewayCredentialRef `json:"credentialRef"`

	// Model is the model served by this gateway. Each Gateway serves
	// exactly one provider/model combination, mirroring the OpenShell
	// gateway model where one gateway = one model.
	Model GatewayModel `json:"model"`
}

// GatewayStatus defines the observed state of a Gateway.
type GatewayStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ConnectionVerified indicates whether the controller has successfully
	// verified connectivity to the gateway endpoint.
	// +optional
	ConnectionVerified bool `json:"connectionVerified,omitempty"`

	// VerifiedCredentialHash is a digest of the credential Secret's data as
	// of the last settled verification. Rotating the credential does not
	// bump the Gateway generation, so this is what tells the controller the
	// verified result no longer describes the credential in the Secret.
	// +optional
	VerifiedCredentialHash string `json:"verifiedCredentialHash,omitempty"`

	// Conditions represent the latest available observations of the
	// Gateway's state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=gw
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.spec.endpoint`
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.spec.model.name`
// +kubebuilder:printcolumn:name="Verified",type=boolean,JSONPath=`.status.connectionVerified`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Gateway is an LLM service endpoint serving exactly one provider/model
// combination. Each Gateway has credentials and a single model declaration.
// This mirrors the OpenShell gateway model where one gateway = one model.
// The controller verifies connectivity on create/update.
type Gateway struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GatewaySpec   `json:"spec,omitempty"`
	Status GatewayStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GatewayList contains a list of Gateway.
type GatewayList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Gateway `json:"items"`
}
