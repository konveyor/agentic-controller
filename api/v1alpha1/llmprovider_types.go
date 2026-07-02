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

// LLMProviderCredentialRef references a Secret containing LLM API credentials.
type LLMProviderCredentialRef struct {
	// SecretName is the name of the Secret in the same namespace.
	// +kubebuilder:validation:MinLength=1
	SecretName string `json:"secretName"`

	// Key is the key within the Secret that contains the credential value.
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// LLMProviderModel declares a model available through this provider.
type LLMProviderModel struct {
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

// LLMProviderSpec defines the desired state of an LLMProvider.
type LLMProviderSpec struct {
	// Endpoint is the base URL of the LLM service.
	// +kubebuilder:validation:MinLength=1
	Endpoint string `json:"endpoint"`

	// CredentialRef references a Secret containing the API credential.
	CredentialRef LLMProviderCredentialRef `json:"credentialRef"`

	// Models is the list of models available through this provider.
	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=name
	Models []LLMProviderModel `json:"models"`
}

// LLMProviderStatus defines the observed state of an LLMProvider.
type LLMProviderStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ConnectionVerified indicates whether the controller has successfully
	// verified connectivity to the provider endpoint.
	// +optional
	ConnectionVerified bool `json:"connectionVerified,omitempty"`

	// DiscoveredModels lists the model names discovered from the provider.
	// +optional
	DiscoveredModels []string `json:"discoveredModels,omitempty"`

	// Conditions represent the latest available observations of the
	// LLMProvider's state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=llm
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.spec.endpoint`
// +kubebuilder:printcolumn:name="Verified",type=boolean,JSONPath=`.status.connectionVerified`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// LLMProvider is an LLM service endpoint with credentials and the set of
// models it serves. Each model declares its context window size and an
// optional tier label. The controller verifies connectivity on create/update.
type LLMProvider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LLMProviderSpec   `json:"spec,omitempty"`
	Status LLMProviderStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// LLMProviderList contains a list of LLMProvider.
type LLMProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LLMProvider `json:"items"`
}
