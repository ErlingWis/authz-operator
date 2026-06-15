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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AuthorizationModelReleaseSpec defines the desired state of AuthorizationModelRelease
type AuthorizationModelReleaseSpec struct {
	// stableModelHash promotes the matching candidate model to stable without rewriting it to OpenFGA.
	// +optional
	// +kubebuilder:validation:MinLength=1
	StableModelHash string `json:"stableModelHash,omitempty"`
}

// AuthorizationModelReleaseStatus defines the observed state of AuthorizationModelRelease.
type AuthorizationModelReleaseStatus struct {
	// stable is the model release used for default authorization query traffic.
	// +optional
	Stable *AuthorizationModelReleaseState `json:"stable,omitempty"`

	// candidate is the most recent valid compiled model written to OpenFGA.
	// +optional
	Candidate *AuthorizationModelReleaseState `json:"candidate,omitempty"`

	// conditions represent the current state of the AuthorizationModelRelease resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// AuthorizationModelReleaseState identifies one published OpenFGA authorization model.
type AuthorizationModelReleaseState struct {
	// modelHash is the hash of the compiled authorization model.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ModelHash string `json:"modelHash"`

	// openFGAStoreID is the OpenFGA store containing this model.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	OpenFGAStoreID string `json:"openfgaStoreID"`

	// openFGAModelID is the OpenFGA authorization model ID.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	OpenFGAModelID string `json:"openfgaModelID"`

	// publishedAt is when the model was written to OpenFGA.
	// +kubebuilder:validation:Required
	PublishedAt metav1.Time `json:"publishedAt"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// AuthorizationModelRelease is the Schema for the authorizationmodelreleases API
type AuthorizationModelRelease struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of AuthorizationModelRelease
	// +required
	Spec AuthorizationModelReleaseSpec `json:"spec"`

	// status defines the observed state of AuthorizationModelRelease
	// +optional
	Status AuthorizationModelReleaseStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// AuthorizationModelReleaseList contains a list of AuthorizationModelRelease
type AuthorizationModelReleaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AuthorizationModelRelease `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AuthorizationModelRelease{}, &AuthorizationModelReleaseList{})
}
