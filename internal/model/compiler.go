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

// Compile builds one deterministic OpenFGA authorization model from module specs.
func Compile(modules []authv1.AuthorizationModule) (AuthorizationModel, error) {
	model := AuthorizationModel{SchemaVersion: schemaVersion}
	types := map[string]*TypeDefinition{}
	moduleByResource := map[string]string{}
	relationNamesByResource := map[string]map[string]struct{}{}

	ensureType := func(name string) *TypeDefinition {
		if typeDef, ok := types[name]; ok {
			return typeDef
		}
		typeDef := &TypeDefinition{Type: name}
		types[name] = typeDef
		return typeDef
	}

	for _, module := range modules {
		resource := module.Spec.Resource
		if resource == "" {
			return AuthorizationModel{}, fmt.Errorf("%s/%s: spec.resource is required", module.Namespace, module.Name)
		}
		if previous, ok := moduleByResource[resource]; ok {
			return AuthorizationModel{}, fmt.Errorf("resource %q is defined by both %s and %s/%s", resource, previous, module.Namespace, module.Name)
		}
		moduleByResource[resource] = fmt.Sprintf("%s/%s", module.Namespace, module.Name)
		ensureType(resource)
		ensureRelationSet(relationNamesByResource, resource)

		for _, role := range module.Spec.Roles {
			for _, subject := range role.Subjects {
				if subject.Type != "" {
					ensureType(subject.Type)
				}
			}
		}
		for _, topology := range module.Spec.Topology {
			for _, resource := range topology.Resources {
				ensureType(resource)
			}
		}
	}

	for _, module := range modules {
		resource := module.Spec.Resource
		relationNames := ensureRelationSet(relationNamesByResource, resource)

		topologyNames := sortedKeys(module.Spec.Topology)
		for _, topologyName := range topologyNames {
			if err := validateRelationName(module, "topology relation", topologyName); err != nil {
				return AuthorizationModel{}, err
			}
			relationNames[topologyName] = struct{}{}
		}

		roleNames := sortedKeys(module.Spec.Roles)
		for _, roleName := range roleNames {
			if err := validateRelationName(module, "role", roleName); err != nil {
				return AuthorizationModel{}, err
			}
			if _, exists := relationNames[roleName]; exists {
				return AuthorizationModel{}, fmt.Errorf("%s/%s: role %q conflicts with an existing relation", module.Namespace, module.Name, roleName)
			}
			relationNames[roleName] = struct{}{}
		}

		permissionNames := sortedKeys(module.Spec.Permissions)
		for _, permissionName := range permissionNames {
			if err := validateRelationName(module, "permission", permissionName); err != nil {
				return AuthorizationModel{}, err
			}
			if _, exists := relationNames[permissionName]; exists {
				return AuthorizationModel{}, fmt.Errorf("%s/%s: permission %q conflicts with an existing relation", module.Namespace, module.Name, permissionName)
			}
			relationNames[permissionName] = struct{}{}
		}
	}

	for i := range modules {
		module := modules[i]
		typeDef := ensureType(module.Spec.Resource)
		if err := compileModule(module, typeDef, relationNamesByResource); err != nil {
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

func compileModule(module authv1.AuthorizationModule, typeDef *TypeDefinition, relationNamesByResource map[string]map[string]struct{}) error {
	relations := map[string]Userset{}
	metadata := map[string]RelationMetadata{}

	topologyNames := sortedKeys(module.Spec.Topology)
	for _, topologyName := range topologyNames {
		topology := module.Spec.Topology[topologyName]
		if len(topology.Resources) == 0 {
			return fmt.Errorf("%s/%s: topology relation %q must reference at least one resource", module.Namespace, module.Name, topologyName)
		}
		relations[topologyName] = Userset{This: &ThisUserset{}}
		metadata[topologyName] = RelationMetadata{
			DirectlyRelatedUserTypes: topologyRelationReferences(topology.Resources),
		}
	}

	roleNames := sortedKeys(module.Spec.Roles)
	for _, roleName := range roleNames {
		role := module.Spec.Roles[roleName]
		userset, err := compileRole(module, roleName, role, relationNamesByResource)
		if err != nil {
			return err
		}
		relations[roleName] = userset
		if len(role.Subjects) > 0 {
			metadata[roleName] = RelationMetadata{
				DirectlyRelatedUserTypes: relationReferences(role.Subjects),
			}
		}
	}

	permissionNames := sortedKeys(module.Spec.Permissions)
	for _, permissionName := range permissionNames {
		permission := module.Spec.Permissions[permissionName]
		userset, err := compilePermission(module, permissionName, permission, relations)
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

func compileRole(module authv1.AuthorizationModule, name string, role authv1.AuthorizationRole, relationNamesByResource map[string]map[string]struct{}) (Userset, error) {
	if len(role.Subjects) == 0 && len(role.Inherited) == 0 {
		return Userset{}, fmt.Errorf("%s/%s: role %q must define subjects or inherited relations", module.Namespace, module.Name, name)
	}

	children := make([]Userset, 0, 1+len(role.Inherited))
	if len(role.Subjects) > 0 {
		children = append(children, Userset{This: &ThisUserset{}})
	}

	for _, inherited := range role.Inherited {
		if err := validateRelationName(module, "inherited via", inherited.Via); err != nil {
			return Userset{}, err
		}
		if err := validateRelationName(module, "inherited relation", inherited.Relation); err != nil {
			return Userset{}, err
		}
		topology, ok := module.Spec.Topology[inherited.Via]
		if !ok {
			return Userset{}, fmt.Errorf("%s/%s: role %q inherits through unknown topology relation %q", module.Namespace, module.Name, name, inherited.Via)
		}
		for _, resource := range topology.Resources {
			targetRelations := relationNamesByResource[resource]
			if _, ok := targetRelations[inherited.Relation]; !ok {
				return Userset{}, fmt.Errorf("%s/%s: role %q inherits relation %q through %q, but resource %q does not define that relation", module.Namespace, module.Name, name, inherited.Relation, inherited.Via, resource)
			}
		}
		children = append(children, Userset{
			TupleToUserset: &TupleToUserset{
				Tupleset:        ObjectRelation{Object: "", Relation: inherited.Via},
				ComputedUserset: ObjectRelation{Object: "", Relation: inherited.Relation},
			},
		})
	}

	if len(children) == 1 {
		return children[0], nil
	}
	return Userset{Union: &UsersetUnion{Child: children}}, nil
}

func compilePermission(module authv1.AuthorizationModule, name string, permission authv1.AuthorizationPermission, relations map[string]Userset) (Userset, error) {
	if len(permission.AnyOf) == 0 {
		return Userset{}, fmt.Errorf("%s/%s: permission %q must reference at least one relation", module.Namespace, module.Name, name)
	}

	children := make([]Userset, 0, len(permission.AnyOf))
	for _, relation := range permission.AnyOf {
		if _, ok := relations[relation]; !ok {
			return Userset{}, fmt.Errorf("%s/%s: permission %q references unknown relation %q", module.Namespace, module.Name, name, relation)
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

func topologyRelationReferences(resources []string) []RelationReference {
	references := make([]RelationReference, 0, len(resources))
	for _, resource := range resources {
		references = append(references, RelationReference{Type: resource})
	}
	sort.Slice(references, func(i, j int) bool {
		return references[i].Type < references[j].Type
	})
	return references
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

func validateRelationName(module authv1.AuthorizationModule, kind, name string) error {
	if name == "" {
		return fmt.Errorf("%s/%s: %s name is required", module.Namespace, module.Name, kind)
	}
	if strings.Contains(name, "#") {
		return fmt.Errorf("%s/%s: %s %q must not contain #", module.Namespace, module.Name, kind, name)
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

func ensureRelationSet(values map[string]map[string]struct{}, key string) map[string]struct{} {
	if relations, ok := values[key]; ok {
		return relations
	}
	relations := map[string]struct{}{}
	values[key] = relations
	return relations
}
