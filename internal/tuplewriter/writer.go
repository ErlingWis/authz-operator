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
	"fmt"

	openfgaclient "github.com/openfga/go-sdk/client"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	authv1 "my.domain/fga/api/v1"
	"my.domain/fga/internal/openfgaconfig"
	"my.domain/fga/internal/tuple"
)

const (
	DefaultNamespace   = "bridder-system"
	DefaultReleaseName = "bridder-authorization-model"
)

type StableModel struct {
	StoreID string
	ModelID string
}

type Resolver interface {
	ResolveStableModel(ctx context.Context) (StableModel, error)
}

type Writer interface {
	WriteTuples(ctx context.Context, stableModel StableModel, changes tuple.Changes) error
}

type Service struct {
	Resolver Resolver
	Writer   Writer
}

func (s *Service) Apply(ctx context.Context, changes tuple.Changes) error {
	stableModel, err := s.Resolver.ResolveStableModel(ctx)
	if err != nil {
		return err
	}
	return s.Writer.WriteTuples(ctx, stableModel, changes)
}

type StableReleaseResolver struct {
	Client      client.Reader
	Namespace   string
	ReleaseName string
}

func (r *StableReleaseResolver) ResolveStableModel(ctx context.Context) (StableModel, error) {
	namespace := r.Namespace
	if namespace == "" {
		namespace = DefaultNamespace
	}
	releaseName := r.ReleaseName
	if releaseName == "" {
		releaseName = DefaultReleaseName
	}

	var release authv1.AuthorizationModelRelease
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: releaseName}, &release); err != nil {
		return StableModel{}, err
	}
	if release.Status.Stable == nil {
		return StableModel{}, fmt.Errorf("authorization model release %s/%s has no stable model", namespace, releaseName)
	}
	if release.Status.Stable.OpenFGAStoreID == "" || release.Status.Stable.OpenFGAModelID == "" {
		return StableModel{}, fmt.Errorf("authorization model release %s/%s stable model is incomplete", namespace, releaseName)
	}
	return StableModel{
		StoreID: release.Status.Stable.OpenFGAStoreID,
		ModelID: release.Status.Stable.OpenFGAModelID,
	}, nil
}

type OpenFGAWriter struct {
	APIURL   string
	APIToken string
}

func (w *OpenFGAWriter) WriteTuples(ctx context.Context, stableModel StableModel, changes tuple.Changes) error {
	if w.APIURL == "" {
		return fmt.Errorf("OpenFGA API URL is required")
	}
	if len(changes.Writes) == 0 && len(changes.Deletes) == 0 {
		return nil
	}

	sdkClient, err := openfgaclient.NewSdkClient(openfgaconfig.New(w.APIURL, w.APIToken, stableModel.StoreID))
	if err != nil {
		return err
	}

	request := openfgaclient.ClientWriteRequest{
		Writes:  make([]openfgaclient.ClientTupleKey, 0, len(changes.Writes)),
		Deletes: make([]openfgaclient.ClientTupleKeyWithoutCondition, 0, len(changes.Deletes)),
	}
	for _, write := range changes.Writes {
		request.Writes = append(request.Writes, openfgaclient.ClientTupleKey{
			User:     write.User,
			Relation: write.Relation,
			Object:   write.Object,
		})
	}
	for _, deleteTuple := range changes.Deletes {
		request.Deletes = append(request.Deletes, openfgaclient.ClientTupleKeyWithoutCondition{
			User:     deleteTuple.User,
			Relation: deleteTuple.Relation,
			Object:   deleteTuple.Object,
		})
	}

	_, err = sdkClient.Write(ctx).
		Options(openfgaclient.ClientWriteOptions{
			AuthorizationModelId: &stableModel.ModelID,
			StoreId:              &stableModel.StoreID,
			Conflict: openfgaclient.ClientWriteConflictOptions{
				OnDuplicateWrites: openfgaclient.CLIENT_WRITE_REQUEST_ON_DUPLICATE_WRITES_IGNORE,
				OnMissingDeletes:  openfgaclient.CLIENT_WRITE_REQUEST_ON_MISSING_DELETES_IGNORE,
			},
		}).
		Body(request).
		Execute()
	return err
}
