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
	"time"

	openfga "github.com/openfga/go-sdk"
	openfgaclient "github.com/openfga/go-sdk/client"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"my.domain/fga/internal/model"
)

const (
	openFGAAPIURLEnv    = "OPENFGA_API_URL"
	openFGAStoreIDEnv   = "OPENFGA_STORE_ID"
	openFGAStoreNameEnv = "OPENFGA_STORE_NAME"

	defaultOpenFGAStoreName = "bridder"

	annotationCandidateModelHash      = "auth.bridder.io/candidate-model-hash"
	annotationCandidateOpenFGAModelID = "auth.bridder.io/candidate-openfga-model-id"
	annotationCandidateOpenFGAStoreID = "auth.bridder.io/candidate-openfga-store-id"
	annotationCandidatePublishedAt    = "auth.bridder.io/candidate-published-at"
	annotationPublishError            = "auth.bridder.io/publish-error"
	annotationPublishErrorAt          = "auth.bridder.io/publish-error-at"
)

// AuthorizationModelPublisher writes compiled authorization models to OpenFGA.
type AuthorizationModelPublisher interface {
	PublishCandidate(ctx context.Context, storeID, storeName string, request openfga.WriteAuthorizationModelRequest) (PublishedAuthorizationModel, error)
}

// PublishedAuthorizationModel identifies a model written to OpenFGA.
type PublishedAuthorizationModel struct {
	StoreID string
	ModelID string
}

// AuthorizationModelPublisherReconciler publishes the compiled model ConfigMap to OpenFGA.
type AuthorizationModelPublisherReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Publisher AuthorizationModelPublisher
	Now       func() time.Time
}

func NewOpenFGAAuthorizationModelPublisherFromEnv() (AuthorizationModelPublisher, error) {
	apiURL := os.Getenv(openFGAAPIURLEnv)
	if apiURL == "" {
		return nil, fmt.Errorf("%s is required", openFGAAPIURLEnv)
	}
	return &OpenFGAAuthorizationModelPublisher{APIURL: apiURL}, nil
}

// OpenFGAAuthorizationModelPublisher writes models using the OpenFGA SDK.
type OpenFGAAuthorizationModelPublisher struct {
	APIURL string
}

func (p *OpenFGAAuthorizationModelPublisher) PublishCandidate(ctx context.Context, storeID, storeName string, request openfga.WriteAuthorizationModelRequest) (PublishedAuthorizationModel, error) {
	if storeName == "" {
		storeName = defaultOpenFGAStoreName
	}

	sdkClient, err := openfgaclient.NewSdkClient(&openfgaclient.ClientConfiguration{
		ApiUrl:  p.APIURL,
		StoreId: storeID,
	})
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

// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;patch;update

func (r *AuthorizationModelPublisherReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if req.Namespace != defaultModelNamespace || req.Name != authorizationModelConfigMapName {
		return ctrl.Result{}, nil
	}

	var configMap corev1.ConfigMap
	if err := r.Get(ctx, req.NamespacedName, &configMap); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	modelHash := configMap.Data[authorizationModelHashKey]
	modelJSON := configMap.Data[authorizationModelConfigMapKey]
	if modelHash == "" || modelJSON == "" {
		return ctrl.Result{}, nil
	}
	if configMap.Annotations[annotationCandidateModelHash] == modelHash {
		return ctrl.Result{}, nil
	}

	request, err := model.ParseWriteRequest([]byte(modelJSON))
	if err != nil {
		if patchErr := r.patchPublishError(ctx, &configMap, err); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{}, nil
	}

	publisher := r.Publisher
	if publisher == nil {
		publisher, err = NewOpenFGAAuthorizationModelPublisherFromEnv()
		if err != nil {
			if patchErr := r.patchPublishError(ctx, &configMap, err); patchErr != nil {
				return ctrl.Result{}, patchErr
			}
			return ctrl.Result{}, nil
		}
	}

	published, err := publisher.PublishCandidate(ctx, r.storeID(&configMap), r.storeName(), request)
	if err != nil {
		if patchErr := r.patchPublishError(ctx, &configMap, err); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{}, nil
	}

	if err := r.patchCandidate(ctx, &configMap, modelHash, published); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Published candidate authorization model", "modelHash", modelHash, "openfgaStoreID", published.StoreID, "openfgaModelID", published.ModelID)
	return ctrl.Result{}, nil
}

func (r *AuthorizationModelPublisherReconciler) storeID(configMap *corev1.ConfigMap) string {
	if storeID := os.Getenv(openFGAStoreIDEnv); storeID != "" {
		return storeID
	}
	return configMap.Annotations[annotationCandidateOpenFGAStoreID]
}

func (r *AuthorizationModelPublisherReconciler) storeName() string {
	if storeName := os.Getenv(openFGAStoreNameEnv); storeName != "" {
		return storeName
	}
	return defaultOpenFGAStoreName
}

func (r *AuthorizationModelPublisherReconciler) patchCandidate(ctx context.Context, configMap *corev1.ConfigMap, modelHash string, published PublishedAuthorizationModel) error {
	patch := client.MergeFrom(configMap.DeepCopy())
	ensureAnnotations(configMap)
	configMap.Annotations[annotationCandidateModelHash] = modelHash
	configMap.Annotations[annotationCandidateOpenFGAModelID] = published.ModelID
	configMap.Annotations[annotationCandidateOpenFGAStoreID] = published.StoreID
	configMap.Annotations[annotationCandidatePublishedAt] = r.now().Format(time.RFC3339)
	delete(configMap.Annotations, annotationPublishError)
	delete(configMap.Annotations, annotationPublishErrorAt)
	return r.Patch(ctx, configMap, patch)
}

func (r *AuthorizationModelPublisherReconciler) patchPublishError(ctx context.Context, configMap *corev1.ConfigMap, err error) error {
	patch := client.MergeFrom(configMap.DeepCopy())
	ensureAnnotations(configMap)
	configMap.Annotations[annotationPublishError] = err.Error()
	configMap.Annotations[annotationPublishErrorAt] = r.now().Format(time.RFC3339)
	return r.Patch(ctx, configMap, patch)
}

func (r *AuthorizationModelPublisherReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now().UTC()
}

func ensureAnnotations(configMap *corev1.ConfigMap) {
	if configMap.Annotations == nil {
		configMap.Annotations = map[string]string{}
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *AuthorizationModelPublisherReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(object client.Object) bool {
			return object.GetNamespace() == defaultModelNamespace && object.GetName() == authorizationModelConfigMapName
		}))).
		Named("authorizationmodelpublisher").
		Complete(r)
}
