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

// AuthorizationComponentSpec defines the desired state of AuthorizationComponent
type AuthorizationComponentSpec struct {
	// resource is the OpenFGA object type this component contributes.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9_]*$`
	Resource string `json:"resource"`

	// topology defines parent-like relationships to other resource types.
	// +optional
	Topology *AuthorizationTopology `json:"topology,omitempty"`

	// roles define directly assignable relationships on this resource.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinProperties=1
	Roles map[string]AuthorizationRole `json:"roles"`

	// permissions define computed relationships derived from roles or other permissions.
	// +optional
	Permissions map[string]AuthorizationPermission `json:"permissions,omitempty"`
}

// AuthorizationTopology defines relationships between this resource and parent resources.
type AuthorizationTopology struct {
	// parent defines the parent relation for this resource.
	// +optional
	Parent *ParentResource `json:"parent,omitempty"`
}

// ParentResource defines a parent object type relation.
type ParentResource struct {
	// resource is the parent OpenFGA object type.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9_]*$`
	Resource string `json:"resource"`
}

// AuthorizationRole defines a directly assignable relationship.
type AuthorizationRole struct {
	// subjects are the object types or usersets that can be directly assigned this role.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Subjects []AuthorizationSubject `json:"subjects"`
}

// AuthorizationSubject defines a type restriction for a directly assignable role.
type AuthorizationSubject struct {
	// type is the OpenFGA subject type, such as user or group.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9_]*$`
	Type string `json:"type"`

	// relation references a userset relation on the subject type, such as group#member.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9_]*$`
	Relation string `json:"relation,omitempty"`
}

// AuthorizationPermission defines a computed relationship.
type AuthorizationPermission struct {
	// anyOf grants the permission when any referenced relation applies.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	AnyOf []string `json:"anyOf"`
}

// AuthorizationComponentStatus defines the observed state of AuthorizationComponent.
type AuthorizationComponentStatus struct {
	// observedModelHash is the hash of the compiled authorization model observed by the controller.
	// +optional
	ObservedModelHash string `json:"observedModelHash,omitempty"`

	// conditions represent the current state of the AuthorizationComponent resource.
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

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// AuthorizationComponent is the Schema for the authorizationcomponents API
type AuthorizationComponent struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of AuthorizationComponent
	// +required
	Spec AuthorizationComponentSpec `json:"spec"`

	// status defines the observed state of AuthorizationComponent
	// +optional
	Status AuthorizationComponentStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// AuthorizationComponentList contains a list of AuthorizationComponent
type AuthorizationComponentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AuthorizationComponent `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AuthorizationComponent{}, &AuthorizationComponentList{})
}
