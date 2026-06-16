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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	pb "github.com/openfga/api/proto/openfga/v1"
	openfga "github.com/openfga/go-sdk"
	"github.com/openfga/openfga/pkg/typesystem"
	"google.golang.org/protobuf/encoding/protojson"

	authv1 "my.domain/fga/api/v1"
)

const schemaVersion = "1.1"

type AuthorizationModel = openfga.WriteAuthorizationModelRequest

// Compile builds one deterministic OpenFGA authorization model from module specs.
func Compile(modules []authv1.AuthorizationModule) (AuthorizationModel, error) {
	model := AuthorizationModel{SchemaVersion: schemaVersion}
	types := map[string]*openfga.TypeDefinition{}
	moduleByResource := map[string]string{}
	relationNamesByResource := map[string]map[string]struct{}{}

	ensureType := func(name string) *openfga.TypeDefinition {
		if typeDef, ok := types[name]; ok {
			return typeDef
		}
		typeDef := &openfga.TypeDefinition{Type: name}
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
		model.TypeDefinitions = append(model.TypeDefinitions, *types[name])
	}

	return model, nil
}

// Fragment returns the compiled OpenFGA type owned by one AuthorizationModule.
func Fragment(module authv1.AuthorizationModule, authorizationModel AuthorizationModel) (AuthorizationModel, error) {
	referencedTypes := map[string]struct{}{module.Spec.Resource: {}}
	for _, role := range module.Spec.Roles {
		for _, subject := range role.Subjects {
			if subject.Type != "" {
				referencedTypes[subject.Type] = struct{}{}
			}
		}
	}
	for _, topology := range module.Spec.Topology {
		for _, resource := range topology.Resources {
			referencedTypes[resource] = struct{}{}
		}
	}

	fragment := AuthorizationModel{SchemaVersion: authorizationModel.SchemaVersion}
	for _, typeDef := range authorizationModel.TypeDefinitions {
		if _, ok := referencedTypes[typeDef.Type]; !ok {
			continue
		}
		if typeDef.Type != module.Spec.Resource {
			typeDef = openfga.TypeDefinition{Type: typeDef.Type}
		}
		fragment.TypeDefinitions = append(fragment.TypeDefinitions, typeDef)
	}
	if len(fragment.TypeDefinitions) > 0 {
		return fragment, nil
	}
	return AuthorizationModel{}, fmt.Errorf("%s/%s: compiled model does not contain resource %q", module.Namespace, module.Name, module.Spec.Resource)
}

// Merge combines compiled module fragments into one deterministic authorization model.
func Merge(fragments []AuthorizationModel) (AuthorizationModel, error) {
	merged := AuthorizationModel{SchemaVersion: schemaVersion}
	types := map[string]openfga.TypeDefinition{}

	for _, fragment := range fragments {
		if fragment.SchemaVersion != schemaVersion {
			return AuthorizationModel{}, fmt.Errorf("schema_version %q is unsupported", fragment.SchemaVersion)
		}
		for _, typeDef := range fragment.TypeDefinitions {
			existing, ok := types[typeDef.Type]
			if ok {
				if isStub(existing) || reflect.DeepEqual(existing, typeDef) {
					types[typeDef.Type] = typeDef
					continue
				}
				if isStub(typeDef) {
					continue
				}
				return AuthorizationModel{}, fmt.Errorf("type %q is defined by multiple authorization module fragments", typeDef.Type)
			}
			types[typeDef.Type] = typeDef
		}
	}

	typeNames := make([]string, 0, len(types))
	for name := range types {
		typeNames = append(typeNames, name)
	}
	sort.Strings(typeNames)
	for _, name := range typeNames {
		merged.TypeDefinitions = append(merged.TypeDefinitions, types[name])
	}
	if len(merged.TypeDefinitions) == 0 {
		return AuthorizationModel{}, fmt.Errorf("type_definitions is required")
	}
	return merged, nil
}

func isStub(typeDef openfga.TypeDefinition) bool {
	return typeDef.Relations == nil && typeDef.Metadata == nil
}

// MarshalStable returns the stable JSON representation used for hashing.
func MarshalStable(model AuthorizationModel) ([]byte, error) {
	return json.Marshal(model)
}

// MarshalWriteRequest returns the JSON payload accepted by OpenFGA's
// WriteAuthorizationModel API and validates it against the SDK request type.
func MarshalWriteRequest(model AuthorizationModel) ([]byte, error) {
	data, err := MarshalRequest(model)
	if err != nil {
		return nil, err
	}
	request, err := ParseWriteRequest(data)
	if err != nil {
		return nil, err
	}
	if err := Validate(request); err != nil {
		return nil, err
	}
	return data, nil
}

// MarshalRequest returns formatted authorization model JSON without semantic validation.
func MarshalRequest(model AuthorizationModel) ([]byte, error) {
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

// Validate runs the same official OpenFGA typesystem validator used by `fga model validate`.
func Validate(model AuthorizationModel) error {
	data, err := json.Marshal(model)
	if err != nil {
		return err
	}

	protoModel := &pb.AuthorizationModel{}
	if err := protojson.Unmarshal(data, protoModel); err != nil {
		return fmt.Errorf("unable to parse authorization model JSON: %w", err)
	}
	if _, err := typesystem.NewAndValidate(context.Background(), protoModel); err != nil {
		return err
	}
	return nil
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

func compileModule(module authv1.AuthorizationModule, typeDef *openfga.TypeDefinition, relationNamesByResource map[string]map[string]struct{}) error {
	relations := map[string]openfga.Userset{}
	metadata := map[string]openfga.RelationMetadata{}

	topologyNames := sortedKeys(module.Spec.Topology)
	for _, topologyName := range topologyNames {
		topology := module.Spec.Topology[topologyName]
		if len(topology.Resources) == 0 {
			return fmt.Errorf("%s/%s: topology relation %q must reference at least one resource", module.Namespace, module.Name, topologyName)
		}
		relations[topologyName] = directUserset()
		metadata[topologyName] = openfga.RelationMetadata{
			DirectlyRelatedUserTypes: ptr(topologyRelationReferences(topology.Resources)),
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
			metadata[roleName] = openfga.RelationMetadata{
				DirectlyRelatedUserTypes: ptr(relationReferences(role.Subjects)),
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

	if len(relations) > 0 {
		typeDef.Relations = ptr(relations)
	}
	if len(metadata) > 0 {
		typeDef.Metadata = &openfga.Metadata{Relations: ptr(metadata)}
	}
	return nil
}

func compileRole(module authv1.AuthorizationModule, name string, role authv1.AuthorizationRole, relationNamesByResource map[string]map[string]struct{}) (openfga.Userset, error) {
	if len(role.Subjects) == 0 && len(role.Inherited) == 0 {
		return openfga.Userset{}, fmt.Errorf("%s/%s: role %q must define subjects or inherited relations", module.Namespace, module.Name, name)
	}

	children := make([]openfga.Userset, 0, 1+len(role.Inherited))
	if len(role.Subjects) > 0 {
		children = append(children, directUserset())
	}

	for _, inherited := range role.Inherited {
		if err := validateRelationName(module, "inherited relation", inherited.Relation); err != nil {
			return openfga.Userset{}, err
		}
		if inherited.Via == "" {
			targetRelations := relationNamesByResource[module.Spec.Resource]
			if _, ok := targetRelations[inherited.Relation]; !ok {
				return openfga.Userset{}, fmt.Errorf("%s/%s: role %q inherits unknown relation %q", module.Namespace, module.Name, name, inherited.Relation)
			}
			children = append(children, openfga.Userset{
				ComputedUserset: ptr(objectRelation(inherited.Relation)),
			})
			continue
		}
		if err := validateRelationName(module, "inherited via", inherited.Via); err != nil {
			return openfga.Userset{}, err
		}
		topology, ok := module.Spec.Topology[inherited.Via]
		if !ok {
			return openfga.Userset{}, fmt.Errorf("%s/%s: role %q inherits through unknown topology relation %q", module.Namespace, module.Name, name, inherited.Via)
		}
		for _, resource := range topology.Resources {
			targetRelations := relationNamesByResource[resource]
			if _, ok := targetRelations[inherited.Relation]; !ok {
				return openfga.Userset{}, fmt.Errorf("%s/%s: role %q inherits relation %q through %q, but resource %q does not define that relation", module.Namespace, module.Name, name, inherited.Relation, inherited.Via, resource)
			}
		}
		children = append(children, openfga.Userset{
			TupleToUserset: &openfga.TupleToUserset{
				Tupleset:        objectRelation(inherited.Via),
				ComputedUserset: objectRelation(inherited.Relation),
			},
		})
	}

	if len(children) == 1 {
		return children[0], nil
	}
	return openfga.Userset{Union: &openfga.Usersets{Child: children}}, nil
}

func compilePermission(module authv1.AuthorizationModule, name string, permission authv1.AuthorizationPermission, relations map[string]openfga.Userset) (openfga.Userset, error) {
	if len(permission.AnyOf) == 0 {
		return openfga.Userset{}, fmt.Errorf("%s/%s: permission %q must reference at least one relation", module.Namespace, module.Name, name)
	}

	children := make([]openfga.Userset, 0, len(permission.AnyOf))
	for _, relation := range permission.AnyOf {
		if _, ok := relations[relation]; !ok {
			return openfga.Userset{}, fmt.Errorf("%s/%s: permission %q references unknown relation %q", module.Namespace, module.Name, name, relation)
		}
		children = append(children, openfga.Userset{
			ComputedUserset: ptr(objectRelation(relation)),
		})
	}

	if len(children) == 1 {
		return children[0], nil
	}
	return openfga.Userset{Union: &openfga.Usersets{Child: children}}, nil
}

func topologyRelationReferences(resources []string) []openfga.RelationReference {
	references := make([]openfga.RelationReference, 0, len(resources))
	for _, resource := range resources {
		references = append(references, openfga.RelationReference{Type: resource})
	}
	sort.Slice(references, func(i, j int) bool {
		return references[i].Type < references[j].Type
	})
	return references
}

func relationReferences(subjects []authv1.AuthorizationSubject) []openfga.RelationReference {
	references := make([]openfga.RelationReference, 0, len(subjects))
	for _, subject := range subjects {
		reference := openfga.RelationReference{Type: subject.Type}
		if subject.Relation != "" {
			reference.Relation = ptr(subject.Relation)
		}
		references = append(references, reference)
	}
	sort.Slice(references, func(i, j int) bool {
		left := references[i].Type + "#" + relation(references[i])
		right := references[j].Type + "#" + relation(references[j])
		return left < right
	})
	return references
}

func directUserset() openfga.Userset {
	return openfga.Userset{This: ptr(map[string]any{})}
}

func objectRelation(relation string) openfga.ObjectRelation {
	return openfga.ObjectRelation{Object: ptr(""), Relation: ptr(relation)}
}

func relation(reference openfga.RelationReference) string {
	if reference.Relation == nil {
		return ""
	}
	return *reference.Relation
}

func ptr[T any](value T) *T {
	return &value
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
