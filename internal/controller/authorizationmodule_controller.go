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
	"encoding/json"
	"fmt"
	"os"
	"time"

	openfgaclient "github.com/openfga/go-sdk/client"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	authv1 "erli.ng/authz-operator/api/v1"
	"erli.ng/authz-operator/internal/model"
	"erli.ng/authz-operator/internal/openfgaconfig"
)

// AuthorizationModuleReconciler reconciles a AuthorizationModule object.
type AuthorizationModuleReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Publisher AuthorizationModelPublisher
	Now       func() time.Time
}

const (
	openFGAClientConfigSecretName = "authz-operator-openfga-client-config"
	openFGAClientConfigKey        = "client-configuration.json"
	openFGAClientConfigHashKey    = "authz.erli.ng/model-hash"
	openFGAClientConfigTimeKey    = "authz.erli.ng/published-at"
	defaultModelNamespace         = "authz-operator-system"
)

// +kubebuilder:rbac:groups=authz.erli.ng,resources=authorizationmodules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=authz.erli.ng,resources=authorizationmodules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=authz.erli.ng,resources=authorizationmodules/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

func (r *AuthorizationModuleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var module authv1.AuthorizationModule
	if err := r.Get(ctx, req.NamespacedName, &module); err != nil {
		if apierrors.IsNotFound(err) {
			return r.publishAuthorizationModel(ctx)
		}
		return ctrl.Result{}, err
	}

	return r.publishAuthorizationModel(ctx)
}

func (r *AuthorizationModuleReconciler) modelNamespace() string {
	return defaultModelNamespace
}

func (r *AuthorizationModuleReconciler) publishAuthorizationModel(ctx context.Context) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var modules authv1.AuthorizationModuleList
	if err := r.List(ctx, &modules); err != nil {
		return ctrl.Result{}, err
	}
	if len(modules.Items) == 0 {
		if err := r.deleteOpenFGAClientConfigSecret(ctx, r.modelNamespace()); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Deleted OpenFGA client configuration", "secret", openFGAClientConfigSecretName, "secretNamespace", r.modelNamespace())
		return ctrl.Result{}, nil
	}

	authorizationModel, err := model.Compile(modules.Items)
	if err != nil {
		return ctrl.Result{}, r.patchFailure(ctx, modules.Items, "ModelCompileFailed", err)
	}

	modelHash, err := model.Hash(authorizationModel)
	if err != nil {
		return ctrl.Result{}, r.patchFailure(ctx, modules.Items, "ModelHashFailed", err)
	}

	modelJSON, err := model.MarshalWriteRequest(authorizationModel)
	if err != nil {
		return ctrl.Result{}, r.patchFailure(ctx, modules.Items, "ModelValidationFailed", err)
	}
	request, err := model.ParseWriteRequest(modelJSON)
	if err != nil {
		return ctrl.Result{}, r.patchFailure(ctx, modules.Items, "ModelValidationFailed", err)
	}

	settings, err := openFGASettingsFromEnv()
	if err != nil {
		return ctrl.Result{}, r.patchFailure(ctx, modules.Items, "ModelPublishFailed", err)
	}

	existingSecret, err := r.openFGAClientConfigSecret(ctx, r.modelNamespace())
	if err != nil {
		return ctrl.Result{}, r.patchFailure(ctx, modules.Items, "ModelPublishFailed", err)
	}

	if published, ok := publishedModelFromSecret(existingSecret, modelHash, settings); ok {
		if err := r.patchPublishedStatuses(ctx, modules.Items, modelHash); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("OpenFGA authorization model already published", "authorizationModuleCount", len(modules.Items), "modelHash", modelHash, "openfgaStoreID", published.StoreID, "openfgaModelID", published.ModelID)
		return ctrl.Result{}, nil
	}

	publisher := r.Publisher
	if publisher == nil {
		publisher, err = NewOpenFGAAuthorizationModelPublisherFromEnv()
		if err != nil {
			return ctrl.Result{}, r.patchFailure(ctx, modules.Items, "ModelPublishFailed", err)
		}
	}

	storeID := settings.StoreID
	if storeID == "" {
		storeID = storeIDFromSecret(existingSecret, settings)
	}
	published, err := publisher.Publish(ctx, storeID, settings.StoreName, request)
	if err != nil {
		return ctrl.Result{}, r.patchFailure(ctx, modules.Items, "ModelPublishFailed", err)
	}

	configJSON, err := openfgaconfig.Marshal(openfgaconfig.New(settings.APIURL, settings.APIToken, published.StoreID, published.ModelID))
	if err != nil {
		return ctrl.Result{}, r.patchFailure(ctx, modules.Items, "ClientConfigWriteFailed", err)
	}
	if err := r.applyOpenFGAClientConfigSecret(ctx, r.modelNamespace(), configJSON, modelHash); err != nil {
		return ctrl.Result{}, r.patchFailure(ctx, modules.Items, "ClientConfigWriteFailed", err)
	}

	if err := r.patchPublishedStatuses(ctx, modules.Items, modelHash); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Published OpenFGA authorization model", "authorizationModuleCount", len(modules.Items), "modelHash", modelHash, "openfgaStoreID", published.StoreID, "openfgaModelID", published.ModelID, "secret", openFGAClientConfigSecretName, "secretNamespace", r.modelNamespace())

	return ctrl.Result{}, nil
}

func (r *AuthorizationModuleReconciler) patchFailure(ctx context.Context, modules []authv1.AuthorizationModule, reason string, err error) error {
	if statusErr := r.patchStatuses(ctx, modules, func(module authv1.AuthorizationModule) metav1.Condition {
		return metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             reason,
			Message:            err.Error(),
			ObservedGeneration: module.Generation,
		}
	}, ""); statusErr != nil {
		return statusErr
	}
	return err
}

func (r *AuthorizationModuleReconciler) patchPublishedStatuses(ctx context.Context, modules []authv1.AuthorizationModule, modelHash string) error {
	return r.patchStatuses(ctx, modules, func(module authv1.AuthorizationModule) metav1.Condition {
		return metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "ModelPublished",
			Message:            "Authorization model published",
			ObservedGeneration: module.Generation,
		}
	}, modelHash)
}

func (r *AuthorizationModuleReconciler) applyOpenFGAClientConfigSecret(ctx context.Context, namespace string, configJSON []byte, modelHash string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      openFGAClientConfigSecretName,
			Namespace: namespace,
		},
	}
	_, err := ctrl.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}
		secret.Labels["app.kubernetes.io/name"] = "authz-operator"
		secret.Labels["app.kubernetes.io/managed-by"] = "authz-operator-controller"

		if secret.Annotations == nil {
			secret.Annotations = map[string]string{}
		}
		secret.Annotations[openFGAClientConfigHashKey] = modelHash
		secret.Annotations[openFGAClientConfigTimeKey] = r.now().Format(time.RFC3339)

		secret.Type = corev1.SecretTypeOpaque
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Data[openFGAClientConfigKey] = configJSON
		return nil
	})
	return err
}

func (r *AuthorizationModuleReconciler) deleteOpenFGAClientConfigSecret(ctx context.Context, namespace string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      openFGAClientConfigSecretName,
			Namespace: namespace,
		},
	}
	if err := r.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (r *AuthorizationModuleReconciler) openFGAClientConfigSecret(ctx context.Context, namespace string) (*corev1.Secret, error) {
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: openFGAClientConfigSecretName}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &secret, nil
}

func (r *AuthorizationModuleReconciler) patchStatus(ctx context.Context, namespacedName types.NamespacedName, condition metav1.Condition, modelHash string) error {
	var module authv1.AuthorizationModule
	if err := r.Get(ctx, namespacedName, &module); err != nil {
		return err
	}

	patch := client.MergeFrom(module.DeepCopy())
	module.Status.ObservedModelHash = modelHash
	setCondition(&module.Status.Conditions, condition)
	return r.Status().Patch(ctx, &module, patch)
}

func (r *AuthorizationModuleReconciler) patchStatuses(ctx context.Context, modules []authv1.AuthorizationModule, conditionFor func(authv1.AuthorizationModule) metav1.Condition, modelHash string) error {
	for _, module := range modules {
		if err := r.patchStatus(ctx, types.NamespacedName{
			Namespace: module.Namespace,
			Name:      module.Name,
		}, conditionFor(module), modelHash); err != nil {
			return err
		}
	}
	return nil
}

func setCondition(conditions *[]metav1.Condition, condition metav1.Condition) {
	condition.LastTransitionTime = metav1.Now()
	for i := range *conditions {
		if (*conditions)[i].Type == condition.Type {
			if (*conditions)[i].Status == condition.Status {
				condition.LastTransitionTime = (*conditions)[i].LastTransitionTime
			}
			(*conditions)[i] = condition
			return
		}
	}
	*conditions = append(*conditions, condition)
}

func (r *AuthorizationModuleReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now().UTC()
}

type openFGASettings struct {
	APIURL    string
	APIToken  string
	StoreID   string
	StoreName string
}

func openFGASettingsFromEnv() (openFGASettings, error) {
	apiURL := os.Getenv(openFGAAPIURLEnv)
	if apiURL == "" {
		return openFGASettings{}, fmt.Errorf("%s is required", openFGAAPIURLEnv)
	}
	storeName := os.Getenv(openFGAStoreNameEnv)
	if storeName == "" {
		storeName = defaultOpenFGAStoreName
	}
	return openFGASettings{
		APIURL:    apiURL,
		APIToken:  os.Getenv(openFGAAPITokenEnv),
		StoreID:   os.Getenv(openFGAStoreIDEnv),
		StoreName: storeName,
	}, nil
}

func publishedModelFromSecret(secret *corev1.Secret, modelHash string, settings openFGASettings) (PublishedAuthorizationModel, bool) {
	if secret == nil || secret.Annotations[openFGAClientConfigHashKey] != modelHash {
		return PublishedAuthorizationModel{}, false
	}
	config, ok := clientConfigFromSecret(secret)
	if !ok {
		return PublishedAuthorizationModel{}, false
	}
	if config.ApiUrl != settings.APIURL {
		return PublishedAuthorizationModel{}, false
	}
	if settings.StoreID != "" && config.StoreId != settings.StoreID {
		return PublishedAuthorizationModel{}, false
	}
	if config.StoreId == "" || config.AuthorizationModelId == "" {
		return PublishedAuthorizationModel{}, false
	}
	if !credentialsMatch(config, settings.APIToken) {
		return PublishedAuthorizationModel{}, false
	}
	return PublishedAuthorizationModel{StoreID: config.StoreId, ModelID: config.AuthorizationModelId}, true
}

func storeIDFromSecret(secret *corev1.Secret, settings openFGASettings) string {
	config, ok := clientConfigFromSecret(secret)
	if !ok {
		return ""
	}
	if config.ApiUrl != settings.APIURL {
		return ""
	}
	return config.StoreId
}

func clientConfigFromSecret(secret *corev1.Secret) (openfgaclient.ClientConfiguration, bool) {
	if secret == nil || len(secret.Data[openFGAClientConfigKey]) == 0 {
		return openfgaclient.ClientConfiguration{}, false
	}
	var config openfgaclient.ClientConfiguration
	if err := json.Unmarshal(secret.Data[openFGAClientConfigKey], &config); err != nil {
		return openfgaclient.ClientConfiguration{}, false
	}
	return config, true
}

func credentialsMatch(config openfgaclient.ClientConfiguration, apiToken string) bool {
	if apiToken == "" {
		return config.Credentials == nil
	}
	if config.Credentials == nil || config.Credentials.Config == nil {
		return false
	}
	return config.Credentials.Config.ApiToken == apiToken
}

func enqueueAuthorizationModelReconcile(_ context.Context, object client.Object) []reconcile.Request {
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{
			Namespace: object.GetNamespace(),
			Name:      object.GetName(),
		},
	}}
}

// SetupWithManager sets up the controller with the Manager.
func (r *AuthorizationModuleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&authv1.AuthorizationModule{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(enqueueAuthorizationModelReconcile), builder.WithPredicates(predicate.NewPredicateFuncs(func(object client.Object) bool {
			return object.GetNamespace() == defaultModelNamespace && object.GetName() == openFGAClientConfigSecretName
		}))).
		Named("authorizationmodule").
		Complete(r)
}
