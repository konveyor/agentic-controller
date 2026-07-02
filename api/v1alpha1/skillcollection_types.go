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

// SkillCollectionSkillRef references a skill by SkillCard CR name,
// OCI image ref, or git source URL. Exactly one of SkillCardRef,
// Image, or Source must be set.
// +kubebuilder:validation:XValidation:rule="(has(self.skillCardRef) ? 1 : 0) + (has(self.image) ? 1 : 0) + (has(self.source) ? 1 : 0) == 1",message="exactly one of skillCardRef, image, or source must be set"
type SkillCollectionSkillRef struct {
	// Name is the local name for this skill within the collection.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// SkillCardRef is the name of a SkillCard CR in the same namespace.
	// +optional
	SkillCardRef string `json:"skillCardRef,omitempty"`

	// Image is an OCI image reference for a pre-built skill artifact.
	// +optional
	Image string `json:"image,omitempty"`

	// Source is a git URL pointing to a skill directory.
	// +optional
	Source string `json:"source,omitempty"`
}

// SkillCollectionSpec defines the desired state of a SkillCollection.
type SkillCollectionSpec struct {
	// Version is the semantic version of the collection.
	// +optional
	Version string `json:"version,omitempty"`

	// Skills is the list of skills in this collection.
	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=name
	Skills []SkillCollectionSkillRef `json:"skills"`
}

// SkillCollectionStatus defines the observed state of a SkillCollection.
type SkillCollectionStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest available observations of the
	// SkillCollection's state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=scol
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// SkillCollection is a group of skills, following the skillimage.io/v1alpha1
// SkillCollection format. Each entry references a skill by OCI image ref,
// git source URL, or SkillCard CR name. The controller creates SkillCard CRs
// for git-sourced entries and reports readiness when all children are resolved.
type SkillCollection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SkillCollectionSpec   `json:"spec,omitempty"`
	Status SkillCollectionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SkillCollectionList contains a list of SkillCollection.
type SkillCollectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SkillCollection `json:"items"`
}
