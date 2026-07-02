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

// SkillCardType indicates whether a skill is on-demand or always-loaded.
// +kubebuilder:validation:Enum=skill;rule
type SkillCardType string

const (
	// SkillCardTypeSkill is an on-demand skill — only its name and description
	// are loaded at startup; the full content activates when the agent invokes it.
	SkillCardTypeSkill SkillCardType = "skill"

	// SkillCardTypeRule is an always-loaded rule — its full content is injected
	// into every agent turn and counts toward the LLM's context budget.
	SkillCardTypeRule SkillCardType = "rule"
)

// SkillCardSpec defines the desired state of a SkillCard.
// Exactly one of Image, Source, or Inline must be set.
// +kubebuilder:validation:XValidation:rule="(has(self.image) ? 1 : 0) + (has(self.source) ? 1 : 0) + (has(self.inline) ? 1 : 0) == 1",message="exactly one of image, source, or inline must be set"
type SkillCardSpec struct {
	// Image is an OCI image reference for a pre-built skill artifact.
	// +optional
	Image string `json:"image,omitempty"`

	// Source is a git URL pointing to a skill directory.
	// The controller clones, builds, and pushes the OCI artifact.
	// +optional
	Source string `json:"source,omitempty"`

	// Inline is raw markdown content for the skill.
	// The controller builds and pushes an OCI artifact from this content.
	// +optional
	Inline string `json:"inline,omitempty"`

	// DisplayName is a human-readable name for the skill.
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// Version is the semantic version of the skill.
	// +optional
	Version string `json:"version,omitempty"`

	// Description is a short description of what the skill does.
	// +optional
	Description string `json:"description,omitempty"`

	// Type indicates whether this is an on-demand skill or an always-loaded rule.
	// Defaults to "skill".
	// +kubebuilder:default=skill
	// +optional
	Type SkillCardType `json:"type,omitempty"`

	// Tags are labels for categorization and discovery.
	// +optional
	// +listType=set
	Tags []string `json:"tags,omitempty"`
}

// SkillCardStatus defines the observed state of a SkillCard.
type SkillCardStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ResolvedImage is the OCI image reference for the resolved skill artifact.
	// Set after successful resolution from any source type.
	// +optional
	ResolvedImage string `json:"resolvedImage,omitempty"`

	// Conditions represent the latest available observations of the SkillCard's state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=skc
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// SkillCard is an individual agent capability or behavioral constraint.
// It follows the skillimage.io/v1alpha1 SkillCard format and supports three
// source types: OCI image ref, git source URL, or inline markdown. All three
// converge to a resolved OCI image ref in status.
type SkillCard struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SkillCardSpec   `json:"spec,omitempty"`
	Status SkillCardStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SkillCardList contains a list of SkillCard.
type SkillCardList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SkillCard `json:"items"`
}
