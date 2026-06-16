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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	authv1 "my.domain/fga/api/v1"
	"my.domain/fga/internal/model"
)

// AuthorizationModuleReconciler reconciles a AuthorizationModule object
type AuthorizationModuleReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	authorizationModelConfigMapName   = "bridder-authorization-model"
	authorizationModelConfigMapKey    = "authorization-model.json"
	authorizationModelFragmentKey     = "authorization-model-fragment.json"
	authorizationModelHashKey         = "modelHash"
	authorizationModelFragmentHashKey = "fragmentHash"
	authorizationModuleConfigMapLabel = "auth.bridder.io/authorization-module"
	defaultModelNamespace             = "bridder-system"
)

// +kubebuilder:rbac:groups=auth.bridder.io,resources=authorizationmodules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=auth.bridder.io,resources=authorizationmodules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.bridder.io,resources=authorizationmodules/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

func (r *AuthorizationModuleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var module authv1.AuthorizationModule
	if err := r.Get(ctx, req.NamespacedName, &module); err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.deleteAuthorizationModuleConfigMap(ctx, req.Namespace, req.Name); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, r.applyAuthorizationModelConfigMapFromFragments(ctx, r.modelNamespace())
		}
		return ctrl.Result{}, err
	}

	var modules authv1.AuthorizationModuleList
	if err := r.List(ctx, &modules); err != nil {
		return ctrl.Result{}, err
	}

	authorizationModel, err := model.Compile(modules.Items)
	if err != nil {
		if statusErr := r.patchStatuses(ctx, modules.Items, func(module authv1.AuthorizationModule) metav1.Condition {
			return metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				Reason:             "ModelCompileFailed",
				Message:            err.Error(),
				ObservedGeneration: module.Generation,
			}
		}, ""); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{}, err
	}

	modelHash, err := model.Hash(authorizationModel)
	if err != nil {
		if statusErr := r.patchStatuses(ctx, modules.Items, func(module authv1.AuthorizationModule) metav1.Condition {
			return metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				Reason:             "ModelHashFailed",
				Message:            err.Error(),
				ObservedGeneration: module.Generation,
			}
		}, ""); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{}, err
	}
	if err := r.applyAuthorizationModuleConfigMaps(ctx, modules.Items, authorizationModel); err != nil {
		if statusErr := r.patchStatuses(ctx, modules.Items, func(module authv1.AuthorizationModule) metav1.Condition {
			return metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				Reason:             "ModelFragmentConfigMapApplyFailed",
				Message:            err.Error(),
				ObservedGeneration: module.Generation,
			}
		}, ""); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{}, err
	}
	if err := r.applyAuthorizationModelConfigMapFromFragments(ctx, r.modelNamespace()); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.patchStatuses(ctx, modules.Items, func(module authv1.AuthorizationModule) metav1.Condition {
		return metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "ModelCompiled",
			Message:            "Authorization model compiled",
			ObservedGeneration: module.Generation,
		}
	}, modelHash); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Compiled authorization model", "authorizationModuleCount", len(modules.Items), "modelHash", modelHash, "configMap", authorizationModelConfigMapName, "configMapNamespace", r.modelNamespace())

	return ctrl.Result{}, nil
}

func (r *AuthorizationModuleReconciler) modelNamespace() string {
	return defaultModelNamespace
}

func (r *AuthorizationModuleReconciler) applyAuthorizationModuleConfigMaps(ctx context.Context, modules []authv1.AuthorizationModule, authorizationModel model.AuthorizationModel) error {
	for i := range modules {
		fragment, err := model.Fragment(modules[i], authorizationModel)
		if err != nil {
			return err
		}
		fragmentHash, err := model.Hash(fragment)
		if err != nil {
			return err
		}
		fragmentJSON, err := model.MarshalRequest(fragment)
		if err != nil {
			return err
		}
		if err := r.applyAuthorizationModuleConfigMap(ctx, &modules[i], fragmentJSON, fragmentHash); err != nil {
			return err
		}
	}
	return nil
}

func (r *AuthorizationModuleReconciler) applyAuthorizationModuleConfigMap(ctx context.Context, module *authv1.AuthorizationModule, modelJSON []byte, modelHash string) error {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      authorizationModuleConfigMapName(module.Name),
			Namespace: module.Namespace,
		},
	}
	_, err := ctrl.CreateOrUpdate(ctx, r.Client, configMap, func() error {
		if configMap.Labels == nil {
			configMap.Labels = map[string]string{}
		}
		configMap.Labels["app.kubernetes.io/name"] = "bridder"
		configMap.Labels["app.kubernetes.io/managed-by"] = "bridder-controller"
		configMap.Labels[authorizationModuleConfigMapLabel] = "true"

		if configMap.Annotations == nil {
			configMap.Annotations = map[string]string{}
		}
		configMap.Annotations["auth.bridder.io/module-name"] = module.Name
		configMap.Annotations["auth.bridder.io/module-namespace"] = module.Namespace
		configMap.Annotations["auth.bridder.io/module-generation"] = fmt.Sprint(module.Generation)

		if err := ctrl.SetControllerReference(module, configMap, r.Scheme); err != nil {
			return err
		}

		if configMap.Data == nil {
			configMap.Data = map[string]string{}
		}
		configMap.Data[authorizationModelFragmentKey] = string(modelJSON)
		configMap.Data[authorizationModelFragmentHashKey] = modelHash
		return nil
	})
	return err
}

func (r *AuthorizationModuleReconciler) applyAuthorizationModelConfigMapFromFragments(ctx context.Context, namespace string) error {
	var modules authv1.AuthorizationModuleList
	if err := r.List(ctx, &modules); err != nil {
		return err
	}
	currentModules := map[string]struct{}{}
	for _, module := range modules.Items {
		currentModules[module.Namespace+"/"+module.Name] = struct{}{}
	}

	var configMaps corev1.ConfigMapList
	if err := r.List(ctx, &configMaps, client.MatchingLabels{authorizationModuleConfigMapLabel: "true"}); err != nil {
		return err
	}

	sort.Slice(configMaps.Items, func(i, j int) bool {
		left := configMaps.Items[i].Namespace + "/" + configMaps.Items[i].Name
		right := configMaps.Items[j].Namespace + "/" + configMaps.Items[j].Name
		return left < right
	})

	fragments := make([]model.AuthorizationModel, 0, len(configMaps.Items))
	for _, configMap := range configMaps.Items {
		owner := configMap.Annotations["auth.bridder.io/module-namespace"] + "/" + configMap.Annotations["auth.bridder.io/module-name"]
		if _, ok := currentModules[owner]; !ok {
			continue
		}
		modelJSON := configMap.Data[authorizationModelFragmentKey]
		if modelJSON == "" {
			continue
		}
		fragment, err := model.ParseWriteRequest([]byte(modelJSON))
		if err != nil {
			return err
		}
		fragments = append(fragments, fragment)
	}
	if len(fragments) == 0 {
		return nil
	}

	authorizationModel, err := model.Merge(fragments)
	if err != nil {
		return err
	}
	modelHash, err := model.Hash(authorizationModel)
	if err != nil {
		return err
	}
	modelJSON, err := model.MarshalWriteRequest(authorizationModel)
	if err != nil {
		return err
	}
	return r.applyAuthorizationModelConfigMap(ctx, namespace, modelJSON, modelHash)
}

func (r *AuthorizationModuleReconciler) applyAuthorizationModelConfigMap(ctx context.Context, namespace string, modelJSON []byte, modelHash string) error {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      authorizationModelConfigMapName,
			Namespace: namespace,
		},
	}
	_, err := ctrl.CreateOrUpdate(ctx, r.Client, configMap, func() error {
		if configMap.Labels == nil {
			configMap.Labels = map[string]string{}
		}
		configMap.Labels["app.kubernetes.io/name"] = "bridder"
		configMap.Labels["app.kubernetes.io/managed-by"] = "bridder-controller"

		if configMap.Data == nil {
			configMap.Data = map[string]string{}
		}
		configMap.Data[authorizationModelConfigMapKey] = string(modelJSON)
		configMap.Data[authorizationModelHashKey] = modelHash
		return nil
	})
	return err
}

func (r *AuthorizationModuleReconciler) deleteAuthorizationModuleConfigMap(ctx context.Context, namespace, name string) error {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      authorizationModuleConfigMapName(name),
			Namespace: namespace,
		},
	}
	if err := r.Delete(ctx, configMap); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func authorizationModuleConfigMapName(name string) string {
	const suffix = "-authorization-model"
	if len(name)+len(suffix) <= 253 {
		return name + suffix
	}
	sum := sha256.Sum256([]byte(name))
	return name[:253-len(suffix)-13] + "-" + hex.EncodeToString(sum[:])[:12] + suffix
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

// SetupWithManager sets up the controller with the Manager.
func (r *AuthorizationModuleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&authv1.AuthorizationModule{}).
		Owns(&corev1.ConfigMap{}).
		Named("authorizationmodule").
		Complete(r)
}
