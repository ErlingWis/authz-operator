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

package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	openfga "github.com/openfga/go-sdk"

	authv1 "my.domain/fga/api/v1"
)

const schemaVersion = "1.1"

// AuthorizationModel is the OpenFGA JSON authorization model accepted by the API.
type AuthorizationModel struct {
	SchemaVersion  string           `json:"schema_version"`
	TypeDefinition []TypeDefinition `json:"type_definitions"`
}

// TypeDefinition is an OpenFGA object type definition.
type TypeDefinition struct {
	Type      string             `json:"type"`
	Relations map[string]Userset `json:"relations,omitempty"`
	Metadata  *TypeMetadata      `json:"metadata,omitempty"`
}

// TypeMetadata carries direct relationship type restrictions.
type TypeMetadata struct {
	Relations map[string]RelationMetadata `json:"relations"`
}

// RelationMetadata carries direct relationship type restrictions for a relation.
type RelationMetadata struct {
	DirectlyRelatedUserTypes []RelationReference `json:"directly_related_user_types"`
}

// RelationReference identifies a directly assignable type or userset.
type RelationReference struct {
	Type     string `json:"type"`
	Relation string `json:"relation,omitempty"`
}

// Userset is an OpenFGA userset expression.
type Userset struct {
	This            *ThisUserset    `json:"this,omitempty"`
	ComputedUserset *ObjectRelation `json:"computedUserset,omitempty"`
	TupleToUserset  *TupleToUserset `json:"tupleToUserset,omitempty"`
	Union           *UsersetUnion   `json:"union,omitempty"`
}

// ThisUserset allows direct relationships.
type ThisUserset struct{}

// ObjectRelation references a relation on the current object.
type ObjectRelation struct {
	Object   string `json:"object"`
	Relation string `json:"relation"`
}

// TupleToUserset follows a relation to another object and computes a relation there.
type TupleToUserset struct {
	Tupleset        ObjectRelation `json:"tupleset"`
	ComputedUserset ObjectRelation `json:"computedUserset"`
}

// UsersetUnion grants access when any child userset matches.
type UsersetUnion struct {
	Child []Userset `json:"child"`
}

// Compile builds one deterministic OpenFGA authorization model from component specs.
func Compile(components []authv1.AuthorizationComponent) (AuthorizationModel, error) {
	model := AuthorizationModel{SchemaVersion: schemaVersion}
	types := map[string]*TypeDefinition{}
	componentByResource := map[string]string{}

	ensureType := func(name string) *TypeDefinition {
		if typeDef, ok := types[name]; ok {
			return typeDef
		}
		typeDef := &TypeDefinition{Type: name}
		types[name] = typeDef
		return typeDef
	}

	for _, component := range components {
		resource := component.Spec.Resource
		if resource == "" {
			return AuthorizationModel{}, fmt.Errorf("%s/%s: spec.resource is required", component.Namespace, component.Name)
		}
		if previous, ok := componentByResource[resource]; ok {
			return AuthorizationModel{}, fmt.Errorf("resource %q is defined by both %s and %s/%s", resource, previous, component.Namespace, component.Name)
		}
		componentByResource[resource] = fmt.Sprintf("%s/%s", component.Namespace, component.Name)
		ensureType(resource)

		for _, role := range component.Spec.Roles {
			for _, subject := range role.Subjects {
				if subject.Type != "" {
					ensureType(subject.Type)
				}
			}
		}
		if component.Spec.Topology != nil && component.Spec.Topology.Parent != nil {
			ensureType(component.Spec.Topology.Parent.Resource)
		}
	}

	for i := range components {
		component := components[i]
		typeDef := ensureType(component.Spec.Resource)
		if err := compileComponent(component, typeDef); err != nil {
			return AuthorizationModel{}, err
		}
	}

	typeNames := make([]string, 0, len(types))
	for name := range types {
		typeNames = append(typeNames, name)
	}
	sort.Strings(typeNames)

	for _, name := range typeNames {
		model.TypeDefinition = append(model.TypeDefinition, *types[name])
	}

	return model, nil
}

// MarshalStable returns the stable JSON representation used for hashing.
func MarshalStable(model AuthorizationModel) ([]byte, error) {
	return json.Marshal(model)
}

// MarshalWriteRequest returns the JSON payload accepted by OpenFGA's
// WriteAuthorizationModel API and validates it against the SDK request type.
func MarshalWriteRequest(model AuthorizationModel) ([]byte, error) {
	data, err := json.MarshalIndent(model, "", "  ")
	if err != nil {
		return nil, err
	}
	if _, err := ParseWriteRequest(data); err != nil {
		return nil, err
	}
	return data, nil
}

// ParseWriteRequest validates authorization model JSON using the OpenFGA SDK type.
func ParseWriteRequest(data []byte) (openfga.WriteAuthorizationModelRequest, error) {
	var request openfga.WriteAuthorizationModelRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return openfga.WriteAuthorizationModelRequest{}, err
	}
	if request.SchemaVersion == "" {
		return openfga.WriteAuthorizationModelRequest{}, fmt.Errorf("schema_version is required")
	}
	if len(request.TypeDefinitions) == 0 {
		return openfga.WriteAuthorizationModelRequest{}, fmt.Errorf("type_definitions is required")
	}
	return request, nil
}

// Hash returns a SHA-256 hash of the model's stable JSON representation.
func Hash(model AuthorizationModel) (string, error) {
	data, err := MarshalStable(model)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func compileComponent(component authv1.AuthorizationComponent, typeDef *TypeDefinition) error {
	relations := map[string]Userset{}
	metadata := map[string]RelationMetadata{}

	if component.Spec.Topology != nil && component.Spec.Topology.Parent != nil {
		parent := component.Spec.Topology.Parent.Resource
		if parent == "" {
			return fmt.Errorf("%s/%s: spec.topology.parent.resource is required", component.Namespace, component.Name)
		}
		relations["parent"] = Userset{This: &ThisUserset{}}
		metadata["parent"] = RelationMetadata{
			DirectlyRelatedUserTypes: []RelationReference{{Type: parent}},
		}
	}

	roleNames := sortedKeys(component.Spec.Roles)
	for _, roleName := range roleNames {
		role := component.Spec.Roles[roleName]
		if err := validateRelationName(component, "role", roleName); err != nil {
			return err
		}
		relations[roleName] = Userset{This: &ThisUserset{}}
		metadata[roleName] = RelationMetadata{
			DirectlyRelatedUserTypes: relationReferences(role.Subjects),
		}
	}

	permissionNames := sortedKeys(component.Spec.Permissions)
	for _, permissionName := range permissionNames {
		permission := component.Spec.Permissions[permissionName]
		if err := validateRelationName(component, "permission", permissionName); err != nil {
			return err
		}
		if _, exists := relations[permissionName]; exists {
			return fmt.Errorf("%s/%s: permission %q conflicts with an existing relation", component.Namespace, component.Name, permissionName)
		}
		userset, err := compilePermission(component, permissionName, permission, relations)
		if err != nil {
			return err
		}
		relations[permissionName] = userset
	}

	typeDef.Relations = relations
	if len(metadata) > 0 {
		typeDef.Metadata = &TypeMetadata{Relations: metadata}
	}
	return nil
}

func compilePermission(component authv1.AuthorizationComponent, name string, permission authv1.AuthorizationPermission, relations map[string]Userset) (Userset, error) {
	if len(permission.AnyOf) == 0 {
		return Userset{}, fmt.Errorf("%s/%s: permission %q must reference at least one relation", component.Namespace, component.Name, name)
	}

	children := make([]Userset, 0, len(permission.AnyOf))
	for _, relation := range permission.AnyOf {
		if _, ok := relations[relation]; !ok {
			return Userset{}, fmt.Errorf("%s/%s: permission %q references unknown relation %q", component.Namespace, component.Name, name, relation)
		}
		children = append(children, Userset{
			ComputedUserset: &ObjectRelation{Object: "", Relation: relation},
		})
	}

	if len(children) == 1 {
		return children[0], nil
	}
	return Userset{Union: &UsersetUnion{Child: children}}, nil
}

func relationReferences(subjects []authv1.AuthorizationSubject) []RelationReference {
	references := make([]RelationReference, 0, len(subjects))
	for _, subject := range subjects {
		references = append(references, RelationReference{Type: subject.Type, Relation: subject.Relation})
	}
	sort.Slice(references, func(i, j int) bool {
		left := references[i].Type + "#" + references[i].Relation
		right := references[j].Type + "#" + references[j].Relation
		return left < right
	})
	return references
}

func validateRelationName(component authv1.AuthorizationComponent, kind, name string) error {
	if name == "" {
		return fmt.Errorf("%s/%s: %s name is required", component.Namespace, component.Name, kind)
	}
	if strings.Contains(name, "#") {
		return fmt.Errorf("%s/%s: %s %q must not contain #", component.Namespace, component.Name, kind, name)
	}
	return nil
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
