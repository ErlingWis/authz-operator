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

package tuplewriter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	authv1 "my.domain/fga/api/v1"
	"my.domain/fga/internal/tuple"
)

func TestStableReleaseResolverResolvesStableModel(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := authv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&authv1.AuthorizationModelRelease{}).
		WithObjects(stableRelease("store-1", "model-1")).
		Build()

	resolver := &StableReleaseResolver{Client: client}
	stableModel, err := resolver.ResolveStableModel(ctx)
	if err != nil {
		t.Fatalf("ResolveStableModel() error = %v", err)
	}

	want := StableModel{StoreID: "store-1", ModelID: "model-1"}
	if diff := cmp.Diff(want, stableModel); diff != "" {
		t.Fatalf("ResolveStableModel() mismatch (-want +got):\n%s", diff)
	}
}

func TestStableReleaseResolverFailsWithoutStableModel(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := authv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}
	release := stableRelease("store-1", "model-1")
	release.Status.Stable = nil
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&authv1.AuthorizationModelRelease{}).
		WithObjects(release).
		Build()

	resolver := &StableReleaseResolver{Client: client}
	if _, err := resolver.ResolveStableModel(ctx); err == nil {
		t.Fatal("ResolveStableModel() error = nil, want error")
	}
}

func TestServiceWritesAgainstStableModel(t *testing.T) {
	ctx := context.Background()
	resolver := &fakeResolver{stableModel: StableModel{StoreID: "store-1", ModelID: "model-1"}}
	writer := &fakeWriter{}
	service := &Service{Resolver: resolver, Writer: writer}
	changes := tuple.Changes{
		Writes: []tuple.TupleKey{{
			User:     "user:alice",
			Relation: "viewer",
			Object:   "document:1",
		}},
		Deletes: []tuple.TupleKey{{
			User:     "group:eng#member",
			Relation: "editor",
			Object:   "document:1",
		}},
	}

	if err := service.Apply(ctx, changes); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if diff := cmp.Diff(resolver.stableModel, writer.stableModel); diff != "" {
		t.Fatalf("stable model mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(changes, writer.changes); diff != "" {
		t.Fatalf("changes mismatch (-want +got):\n%s", diff)
	}
}

func TestServiceReturnsResolverError(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("no stable model")
	service := &Service{
		Resolver: &fakeResolver{err: wantErr},
		Writer:   &fakeWriter{},
	}

	if err := service.Apply(ctx, tuple.Changes{}); !errors.Is(err, wantErr) {
		t.Fatalf("Apply() error = %v, want %v", err, wantErr)
	}
}

type fakeResolver struct {
	stableModel StableModel
	err         error
}

func (r *fakeResolver) ResolveStableModel(context.Context) (StableModel, error) {
	if r.err != nil {
		return StableModel{}, r.err
	}
	return r.stableModel, nil
}

type fakeWriter struct {
	stableModel StableModel
	changes     tuple.Changes
}

func (w *fakeWriter) WriteTuples(_ context.Context, stableModel StableModel, changes tuple.Changes) error {
	w.stableModel = stableModel
	w.changes = changes
	return nil
}

func stableRelease(storeID, modelID string) *authv1.AuthorizationModelRelease {
	return &authv1.AuthorizationModelRelease{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: DefaultNamespace,
			Name:      DefaultReleaseName,
		},
		Spec: authv1.AuthorizationModelReleaseSpec{},
		Status: authv1.AuthorizationModelReleaseStatus{
			Stable: &authv1.AuthorizationModelReleaseState{
				ModelHash:      "hash-1",
				OpenFGAStoreID: storeID,
				OpenFGAModelID: modelID,
				PublishedAt:    metav1.NewTime(time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)),
			},
		},
	}
}
