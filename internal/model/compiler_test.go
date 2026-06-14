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
	"testing"

	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	authv1 "my.domain/fga/api/v1"
)

func TestCompile(t *testing.T) {
	component := authv1.AuthorizationComponent{
		ObjectMeta: metav1.ObjectMeta{Name: "project-auth", Namespace: "platform"},
		Spec: authv1.AuthorizationComponentSpec{
			Resource: "project",
			Topology: &authv1.AuthorizationTopology{
				Parent: &authv1.ParentResource{Resource: "organization"},
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

	got, err := Compile([]authv1.AuthorizationComponent{component})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	want := AuthorizationModel{
		SchemaVersion: "1.1",
		TypeDefinition: []TypeDefinition{
			{Type: "group"},
			{Type: "organization"},
			{
				Type: "project",
				Relations: map[string]Userset{
					"delete": {
						ComputedUserset: &ObjectRelation{Object: "", Relation: "owner"},
					},
					"editor": {
						This: &ThisUserset{},
					},
					"owner": {
						This: &ThisUserset{},
					},
					"parent": {
						This: &ThisUserset{},
					},
					"view": {
						Union: &UsersetUnion{Child: []Userset{
							{ComputedUserset: &ObjectRelation{Object: "", Relation: "owner"}},
							{ComputedUserset: &ObjectRelation{Object: "", Relation: "editor"}},
						}},
					},
				},
				Metadata: &TypeMetadata{Relations: map[string]RelationMetadata{
					"editor": {
						DirectlyRelatedUserTypes: []RelationReference{
							{Type: "group", Relation: "member"},
							{Type: "user"},
						},
					},
					"owner": {
						DirectlyRelatedUserTypes: []RelationReference{{Type: "user"}},
					},
					"parent": {
						DirectlyRelatedUserTypes: []RelationReference{{Type: "organization"}},
					},
				}},
			},
			{Type: "user"},
		},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("Compile() mismatch (-want +got):\n%s", diff)
	}
}

func TestCompileRejectsDuplicateResources(t *testing.T) {
	components := []authv1.AuthorizationComponent{
		component("first", "project"),
		component("second", "project"),
	}

	if _, err := Compile(components); err == nil {
		t.Fatal("Compile() error = nil, want duplicate resource error")
	}
}

func TestCompileRejectsUnknownPermissionReference(t *testing.T) {
	authComponent := component("project-auth", "project")
	authComponent.Spec.Permissions = map[string]authv1.AuthorizationPermission{
		"view": {AnyOf: []string{"missing"}},
	}

	if _, err := Compile([]authv1.AuthorizationComponent{authComponent}); err == nil {
		t.Fatal("Compile() error = nil, want unknown relation error")
	}
}

func TestHashIsStable(t *testing.T) {
	first := component("project-auth", "project")
	first.Spec.Roles["editor"] = authv1.AuthorizationRole{
		Subjects: []authv1.AuthorizationSubject{{Type: "group", Relation: "member"}, {Type: "user"}},
	}

	second := component("project-auth", "project")
	second.Spec.Roles["editor"] = authv1.AuthorizationRole{
		Subjects: []authv1.AuthorizationSubject{{Type: "user"}, {Type: "group", Relation: "member"}},
	}

	firstModel, err := Compile([]authv1.AuthorizationComponent{first})
	if err != nil {
		t.Fatalf("Compile(first) error = %v", err)
	}
	secondModel, err := Compile([]authv1.AuthorizationComponent{second})
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
	authorizationModel, err := Compile([]authv1.AuthorizationComponent{component("project-auth", "project")})
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

func component(name, resource string) authv1.AuthorizationComponent {
	return authv1.AuthorizationComponent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "platform"},
		Spec: authv1.AuthorizationComponentSpec{
			Resource: resource,
			Roles: map[string]authv1.AuthorizationRole{
				"owner": {
					Subjects: []authv1.AuthorizationSubject{{Type: "user"}},
				},
			},
		},
	}
}
