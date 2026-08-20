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

	// SubPath selects one skill from a source that holds several, naming its
	// directory within the image or repository. A SkillCard is always one
	// skill, so a source holding several without a SubPath is an error.
	// Only meaningful with Image or Source.
	// +optional
	SubPath string `json:"subPath,omitempty"`

	// Source is a git URL. The skill loader clones it at pod start; nothing
	// is built.
	// +optional
	Source string `json:"source,omitempty"`

	// Ref is the branch, tag or commit to check out from Source. Empty
	// clones the default branch, which makes the run unreproducible, and the
	// loader warns when it is unset.
	// Only meaningful with Source.
	// +optional
	Ref string `json:"ref,omitempty"`

	// Inline is raw markdown content for the skill, delivered as a ConfigMap.
	// A ConfigMap key cannot hold a path separator, so an inline skill is a
	// single SKILL.md and cannot ship supporting files.
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

	// Type is this skill's load policy. Defaults to "skill".
	//
	// It lives here rather than in SKILL.md: the Agent Skills spec's field set
	// is closed, so declaring it in content would break the format (ADR 0015).
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
	// Only an image source resolves to one; for inline and git it stays empty
	// because there is no image, so read DeliveryMode rather than inferring
	// from this being unset.
	// +optional
	ResolvedImage string `json:"resolvedImage,omitempty"`

	// DeliveryMode is how this skill reaches the pod, so that "not resolved
	// yet" is distinguishable from "resolved, just not to an image".
	// +kubebuilder:validation:Enum=image;inline;source
	// +optional
	DeliveryMode string `json:"deliveryMode,omitempty"`

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
