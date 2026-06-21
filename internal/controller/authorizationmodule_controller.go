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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	authv1 "erli.ng/authz-operator/api/v1"
	"erli.ng/authz-operator/internal/model"
)

// AuthorizationModuleReconciler reconciles a AuthorizationModule object
type AuthorizationModuleReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	authorizationModelConfigMapName = "bridder-authorization-model"
	authorizationModelConfigMapKey  = "authorization-model.json"
	authorizationModelHashKey       = "modelHash"
	defaultModelNamespace           = "bridder-system"
)

// +kubebuilder:rbac:groups=authz.erli.ng,resources=authorizationmodules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=authz.erli.ng,resources=authorizationmodules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=authz.erli.ng,resources=authorizationmodules/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

func (r *AuthorizationModuleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var module authv1.AuthorizationModule
	if err := r.Get(ctx, req.NamespacedName, &module); err != nil {
		if apierrors.IsNotFound(err) {
			return r.recompileAuthorizationModel(ctx)
		}
		return ctrl.Result{}, err
	}

	return r.recompileAuthorizationModel(ctx)
}

func (r *AuthorizationModuleReconciler) modelNamespace() string {
	return defaultModelNamespace
}

func (r *AuthorizationModuleReconciler) recompileAuthorizationModel(ctx context.Context) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var modules authv1.AuthorizationModuleList
	if err := r.List(ctx, &modules); err != nil {
		return ctrl.Result{}, err
	}
	if len(modules.Items) == 0 {
		if err := r.deleteAuthorizationModelConfigMap(ctx, r.modelNamespace()); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Deleted authorization model", "configMap", authorizationModelConfigMapName, "configMapNamespace", r.modelNamespace())
		return ctrl.Result{}, nil
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

	modelJSON, err := model.MarshalWriteRequest(authorizationModel)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.applyAuthorizationModelConfigMap(ctx, r.modelNamespace(), modelJSON, modelHash); err != nil {
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

func (r *AuthorizationModuleReconciler) deleteAuthorizationModelConfigMap(ctx context.Context, namespace string) error {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      authorizationModelConfigMapName,
			Namespace: namespace,
		},
	}
	if err := r.Delete(ctx, configMap); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
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
