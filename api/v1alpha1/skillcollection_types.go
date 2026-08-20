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

// LabelSkillCollection marks SkillCards a collection generated, so that
// pruning finds them without guessing from names.
//
// It lives here because the enumeration Job writes it and the controller
// prunes by it. A label the two agreed on only by string equality would drift
// silently: the List comes back empty and every generated card is orphaned
// instead of pruned, with nothing to compile against.
const LabelSkillCollection = "konveyor.io/skillcollection"

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

	// Source is a git URL. The skill loader clones it at pod start.
	// +optional
	Source string `json:"source,omitempty"`

	// Ref is the branch, tag or commit to check out from Source. Empty
	// clones the default branch, leaving the run unreproducible.
	// Only meaningful with Source.
	// +optional
	Ref string `json:"ref,omitempty"`

	// SubPath selects one skill from a source holding several, naming its
	// directory within the image or repository.
	// Only meaningful with Image or Source.
	// +optional
	SubPath string `json:"subPath,omitempty"`

	// Type is the load policy for this entry's skill. Defaults to "skill".
	// Ignored when SkillCardRef is set, since the card carries its own.
	// +optional
	Type SkillCardType `json:"type,omitempty"`
}

// SkillCollectionSpec defines the desired state of a SkillCollection.
// Set either Image or Skills.
// +kubebuilder:validation:XValidation:rule="has(self.image) != has(self.skills)",message="set either image or skills, not both"
type SkillCollectionSpec struct {
	// Version is the semantic version of the collection.
	// +optional
	Version string `json:"version,omitempty"`

	// Image is an OCI image holding one or more skills. The controller
	// enumerates it with a short-lived Job and creates a SkillCard per skill
	// it finds, owned by this collection, so a user points at a source once
	// rather than writing a card per skill.
	// +optional
	Image string `json:"image,omitempty"`

	// Type is the load policy applied to every skill enumerated from Image.
	// Defaults to "skill". Per-skill policy within one image is not yet
	// expressible; see ADR 0015.
	// +optional
	Type SkillCardType `json:"type,omitempty"`

	// Skills is an explicit list, for grouping skills that already exist.
	//
	// MinItems, because the rule above is satisfied by a list that is present
	// and empty: without it `skills: []` is admitted and the collection is
	// simply never Ready, where the field used to be rejected outright.
	// +optional
	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=name
	Skills []SkillCollectionSkillRef `json:"skills,omitempty"`
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

	// ResolvedSkills names the SkillCards this collection owns, whether
	// enumerated from Image or listed explicitly.
	// +optional
	// +listType=set
	ResolvedSkills []string `json:"resolvedSkills,omitempty"`
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
