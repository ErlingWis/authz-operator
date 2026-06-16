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
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	openfga "github.com/openfga/go-sdk"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	authv1 "my.domain/fga/api/v1"
)

func TestCompile(t *testing.T) {
	module := authv1.AuthorizationModule{
		ObjectMeta: metav1.ObjectMeta{Name: "project-auth", Namespace: "platform"},
		Spec: authv1.AuthorizationModuleSpec{
			Resource: "project",
			Topology: map[string]authv1.TopologyRelation{
				"parent": {Resources: []string{"organization"}},
			},
			Roles: map[string]authv1.AuthorizationRole{
				"owner": {
					Subjects: []authv1.AuthorizationSubject{{Type: "user"}},
				},
				"editor": {
					Subjects: []authv1.AuthorizationSubject{
						{Type: "user"},
						{Type: "group", Relation: "member"},
					},
				},
			},
			Permissions: map[string]authv1.AuthorizationPermission{
				"view": {
					AnyOf: []string{"owner", "editor"},
				},
				"delete": {
					AnyOf: []string{"owner"},
				},
			},
		},
	}

	got, err := Compile([]authv1.AuthorizationModule{module})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	want := AuthorizationModel{
		SchemaVersion: "1.1",
		TypeDefinitions: []openfga.TypeDefinition{
			{Type: "group"},
			{Type: "organization"},
			{
				Type: "project",
				Relations: ptr(map[string]openfga.Userset{
					"delete": {
						ComputedUserset: ptr(objectRelation("owner")),
					},
					"editor": directUserset(),
					"owner":  directUserset(),
					"parent": directUserset(),
					"view": {
						Union: &openfga.Usersets{Child: []openfga.Userset{
							{ComputedUserset: ptr(objectRelation("owner"))},
							{ComputedUserset: ptr(objectRelation("editor"))},
						}},
					},
				}),
				Metadata: &openfga.Metadata{Relations: ptr(map[string]openfga.RelationMetadata{
					"editor": {
						DirectlyRelatedUserTypes: ptr([]openfga.RelationReference{
							{Type: "group", Relation: ptr("member")},
							{Type: "user"},
						}),
					},
					"owner": {
						DirectlyRelatedUserTypes: ptr([]openfga.RelationReference{{Type: "user"}}),
					},
					"parent": {
						DirectlyRelatedUserTypes: ptr([]openfga.RelationReference{{Type: "organization"}}),
					},
				})},
			},
			{Type: "user"},
		},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("Compile() mismatch (-want +got):\n%s", diff)
	}
}

func TestCompileRejectsDuplicateResources(t *testing.T) {
	modules := []authv1.AuthorizationModule{
		module("first"),
		module("second"),
	}

	if _, err := Compile(modules); err == nil {
		t.Fatal("Compile() error = nil, want duplicate resource error")
	}
}

func TestCompileRejectsUnknownPermissionReference(t *testing.T) {
	authModule := module("project-auth")
	authModule.Spec.Permissions = map[string]authv1.AuthorizationPermission{
		"view": {AnyOf: []string{"missing"}},
	}

	if _, err := Compile([]authv1.AuthorizationModule{authModule}); err == nil {
		t.Fatal("Compile() error = nil, want unknown relation error")
	}
}

func TestCompileInheritedRole(t *testing.T) {
	folder := authv1.AuthorizationModule{
		ObjectMeta: metav1.ObjectMeta{Name: "folder-auth", Namespace: "platform"},
		Spec: authv1.AuthorizationModuleSpec{
			Resource: "folder",
			Roles: map[string]authv1.AuthorizationRole{
				"reader": {
					Subjects: []authv1.AuthorizationSubject{{Type: "user"}},
				},
			},
		},
	}
	file := authv1.AuthorizationModule{
		ObjectMeta: metav1.ObjectMeta{Name: "file-auth", Namespace: "platform"},
		Spec: authv1.AuthorizationModuleSpec{
			Resource: "file",
			Topology: map[string]authv1.TopologyRelation{
				"parent": {Resources: []string{"folder"}},
			},
			Roles: map[string]authv1.AuthorizationRole{
				"reader": {
					Inherited: []authv1.InheritedRelation{{Via: "parent", Relation: "reader"}},
				},
			},
			Permissions: map[string]authv1.AuthorizationPermission{
				"read": {AnyOf: []string{"reader"}},
			},
		},
	}

	got, err := Compile([]authv1.AuthorizationModule{folder, file})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	fileRelations := typeDefinition(t, got, "file").Relations
	wantReader := openfga.Userset{
		TupleToUserset: &openfga.TupleToUserset{
			Tupleset:        objectRelation("parent"),
			ComputedUserset: objectRelation("reader"),
		},
	}
	if diff := cmp.Diff(wantReader, (*fileRelations)["reader"]); diff != "" {
		t.Fatalf("reader relation mismatch (-want +got):\n%s", diff)
	}
}

func TestCompileMixedDirectAndInheritedRole(t *testing.T) {
	folder := authv1.AuthorizationModule{
		ObjectMeta: metav1.ObjectMeta{Name: "folder-auth", Namespace: "platform"},
		Spec: authv1.AuthorizationModuleSpec{
			Resource: "folder",
			Roles: map[string]authv1.AuthorizationRole{
				"reader": {Subjects: []authv1.AuthorizationSubject{{Type: "user"}}},
			},
		},
	}
	file := authv1.AuthorizationModule{
		ObjectMeta: metav1.ObjectMeta{Name: "file-auth", Namespace: "platform"},
		Spec: authv1.AuthorizationModuleSpec{
			Resource: "file",
			Topology: map[string]authv1.TopologyRelation{
				"parent": {Resources: []string{"folder"}},
			},
			Roles: map[string]authv1.AuthorizationRole{
				"reader": {
					Subjects:  []authv1.AuthorizationSubject{{Type: "user"}},
					Inherited: []authv1.InheritedRelation{{Via: "parent", Relation: "reader"}},
				},
			},
		},
	}

	got, err := Compile([]authv1.AuthorizationModule{folder, file})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	reader := (*typeDefinition(t, got, "file").Relations)["reader"]
	want := openfga.Userset{Union: &openfga.Usersets{Child: []openfga.Userset{
		directUserset(),
		{TupleToUserset: &openfga.TupleToUserset{
			Tupleset:        objectRelation("parent"),
			ComputedUserset: objectRelation("reader"),
		}},
	}}}
	if diff := cmp.Diff(want, reader); diff != "" {
		t.Fatalf("reader relation mismatch (-want +got):\n%s", diff)
	}
}

func TestCompileRejectsUnknownInheritedVia(t *testing.T) {
	authModule := module("project-auth")
	authModule.Spec.Roles["owner"] = authv1.AuthorizationRole{
		Inherited: []authv1.InheritedRelation{{Via: "parent", Relation: "owner"}},
	}

	_, err := Compile([]authv1.AuthorizationModule{authModule})
	if err == nil || !strings.Contains(err.Error(), "unknown topology relation") {
		t.Fatalf("Compile() error = %v, want unknown topology relation", err)
	}
}

func TestCompileRejectsUnknownInheritedTargetRelation(t *testing.T) {
	folder := authv1.AuthorizationModule{
		ObjectMeta: metav1.ObjectMeta{Name: "folder-auth", Namespace: "platform"},
		Spec: authv1.AuthorizationModuleSpec{
			Resource: "folder",
			Roles: map[string]authv1.AuthorizationRole{
				"reader": {Subjects: []authv1.AuthorizationSubject{{Type: "user"}}},
			},
		},
	}
	file := authv1.AuthorizationModule{
		ObjectMeta: metav1.ObjectMeta{Name: "file-auth", Namespace: "platform"},
		Spec: authv1.AuthorizationModuleSpec{
			Resource: "file",
			Topology: map[string]authv1.TopologyRelation{
				"parent": {Resources: []string{"folder"}},
			},
			Roles: map[string]authv1.AuthorizationRole{
				"reader": {
					Inherited: []authv1.InheritedRelation{{Via: "parent", Relation: "missing"}},
				},
			},
		},
	}

	_, err := Compile([]authv1.AuthorizationModule{folder, file})
	if err == nil || !strings.Contains(err.Error(), "does not define that relation") {
		t.Fatalf("Compile() error = %v, want missing target relation", err)
	}
}

func TestCompileRejectsEmptyTopologyResources(t *testing.T) {
	authModule := module("project-auth")
	authModule.Spec.Topology = map[string]authv1.TopologyRelation{
		"parent": {},
	}

	_, err := Compile([]authv1.AuthorizationModule{authModule})
	if err == nil || !strings.Contains(err.Error(), "must reference at least one resource") {
		t.Fatalf("Compile() error = %v, want empty topology resources", err)
	}
}

func TestCompileRejectsEmptyRole(t *testing.T) {
	authModule := module("project-auth")
	authModule.Spec.Roles["empty"] = authv1.AuthorizationRole{}

	_, err := Compile([]authv1.AuthorizationModule{authModule})
	if err == nil || !strings.Contains(err.Error(), "must define subjects or inherited relations") {
		t.Fatalf("Compile() error = %v, want empty role", err)
	}
}

func TestCompileRejectsRoleTopologyConflict(t *testing.T) {
	authModule := module("project-auth")
	authModule.Spec.Topology = map[string]authv1.TopologyRelation{
		"owner": {Resources: []string{"organization"}},
	}

	_, err := Compile([]authv1.AuthorizationModule{authModule})
	if err == nil || !strings.Contains(err.Error(), "conflicts with an existing relation") {
		t.Fatalf("Compile() error = %v, want role topology conflict", err)
	}
}

func TestHashIsStable(t *testing.T) {
	first := module("project-auth")
	first.Spec.Roles["editor"] = authv1.AuthorizationRole{
		Subjects: []authv1.AuthorizationSubject{{Type: "group", Relation: "member"}, {Type: "user"}},
	}

	second := module("project-auth")
	second.Spec.Roles["editor"] = authv1.AuthorizationRole{
		Subjects: []authv1.AuthorizationSubject{{Type: "user"}, {Type: "group", Relation: "member"}},
	}

	firstModel, err := Compile([]authv1.AuthorizationModule{first})
	if err != nil {
		t.Fatalf("Compile(first) error = %v", err)
	}
	secondModel, err := Compile([]authv1.AuthorizationModule{second})
	if err != nil {
		t.Fatalf("Compile(second) error = %v", err)
	}

	firstHash, err := Hash(firstModel)
	if err != nil {
		t.Fatalf("Hash(first) error = %v", err)
	}
	secondHash, err := Hash(secondModel)
	if err != nil {
		t.Fatalf("Hash(second) error = %v", err)
	}

	if firstHash != secondHash {
		t.Fatalf("Hash() = %q and %q, want equal hashes", firstHash, secondHash)
	}

	data, err := MarshalStable(firstModel)
	if err != nil {
		t.Fatalf("MarshalStable() error = %v", err)
	}
	if !json.Valid(data) {
		t.Fatalf("MarshalStable() returned invalid JSON: %s", data)
	}
}

func TestMarshalWriteRequestValidatesAgainstOpenFGASDK(t *testing.T) {
	authorizationModel, err := Compile([]authv1.AuthorizationModule{module("project-auth")})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	data, err := MarshalWriteRequest(authorizationModel)
	if err != nil {
		t.Fatalf("MarshalWriteRequest() error = %v", err)
	}
	if !json.Valid(data) {
		t.Fatalf("MarshalWriteRequest() returned invalid JSON: %s", data)
	}

	request, err := ParseWriteRequest(data)
	if err != nil {
		t.Fatalf("ParseWriteRequest() error = %v", err)
	}
	if request.SchemaVersion != "1.1" {
		t.Fatalf("SchemaVersion = %q, want 1.1", request.SchemaVersion)
	}
	if len(request.TypeDefinitions) != 2 {
		t.Fatalf("TypeDefinitions length = %d, want 2", len(request.TypeDefinitions))
	}
}

func module(name string) authv1.AuthorizationModule {
	return authv1.AuthorizationModule{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "platform"},
		Spec: authv1.AuthorizationModuleSpec{
			Resource: "project",
			Roles: map[string]authv1.AuthorizationRole{
				"owner": {
					Subjects: []authv1.AuthorizationSubject{{Type: "user"}},
				},
			},
		},
	}
}

func typeDefinition(t *testing.T, model AuthorizationModel, name string) openfga.TypeDefinition {
	t.Helper()
	for _, typeDef := range model.TypeDefinitions {
		if typeDef.Type == name {
			return typeDef
		}
	}
	t.Fatalf("type definition %q not found", name)
	return openfga.TypeDefinition{}
}
