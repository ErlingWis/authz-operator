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

package controller

import (
	"context"
	"fmt"
	"os"

	openfga "github.com/openfga/go-sdk"
	openfgaclient "github.com/openfga/go-sdk/client"

	"erli.ng/authz-operator/internal/openfgaconfig"
)

const (
	openFGAAPIURLEnv    = "OPENFGA_API_URL"
	openFGAAPITokenEnv  = "OPENFGA_API_TOKEN"
	openFGAStoreIDEnv   = "OPENFGA_STORE_ID"
	openFGAStoreNameEnv = "OPENFGA_STORE_NAME"

	defaultOpenFGAStoreName = "authz-operator"
)

// AuthorizationModelPublisher writes compiled authorization models to OpenFGA.
type AuthorizationModelPublisher interface {
	Publish(ctx context.Context, storeID, storeName string, request openfga.WriteAuthorizationModelRequest) (PublishedAuthorizationModel, error)
}

// PublishedAuthorizationModel identifies a model written to OpenFGA.
type PublishedAuthorizationModel struct {
	StoreID string
	ModelID string
}

func NewOpenFGAAuthorizationModelPublisherFromEnv() (AuthorizationModelPublisher, error) {
	apiURL := os.Getenv(openFGAAPIURLEnv)
	if apiURL == "" {
		return nil, fmt.Errorf("%s is required", openFGAAPIURLEnv)
	}
	return &OpenFGAAuthorizationModelPublisher{
		APIURL:   apiURL,
		APIToken: os.Getenv(openFGAAPITokenEnv),
	}, nil
}

// OpenFGAAuthorizationModelPublisher writes models using the OpenFGA SDK.
type OpenFGAAuthorizationModelPublisher struct {
	APIURL   string
	APIToken string
}

func (p *OpenFGAAuthorizationModelPublisher) Publish(ctx context.Context, storeID, storeName string, request openfga.WriteAuthorizationModelRequest) (PublishedAuthorizationModel, error) {
	if storeName == "" {
		storeName = defaultOpenFGAStoreName
	}

	sdkClient, err := openfgaclient.NewSdkClient(openfgaconfig.New(p.APIURL, p.APIToken, storeID, ""))
	if err != nil {
		return PublishedAuthorizationModel{}, err
	}

	if storeID == "" {
		store, err := sdkClient.CreateStore(ctx).
			Body(openfgaclient.ClientCreateStoreRequest{Name: storeName}).
			Execute()
		if err != nil {
			return PublishedAuthorizationModel{}, err
		}
		storeID = store.GetId()
	}

	response, err := sdkClient.WriteAuthorizationModel(ctx).
		Options(openfgaclient.ClientWriteAuthorizationModelOptions{StoreId: &storeID}).
		Body(request).
		Execute()
	if err != nil {
		return PublishedAuthorizationModel{}, err
	}

	return PublishedAuthorizationModel{
		StoreID: storeID,
		ModelID: response.GetAuthorizationModelId(),
	}, nil
}
