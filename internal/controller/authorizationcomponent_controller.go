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

	authv1 "my.domain/fga/api/v1"
	"my.domain/fga/internal/model"
)

// AuthorizationComponentReconciler reconciles a AuthorizationComponent object
type AuthorizationComponentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	authorizationModelConfigMapName = "bridder-authorization-model"
	authorizationModelConfigMapKey  = "authorization-model.json"
	authorizationModelHashKey       = "modelHash"
)

// +kubebuilder:rbac:groups=auth.bridder.io,resources=authorizationcomponents,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=auth.bridder.io,resources=authorizationcomponents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=auth.bridder.io,resources=authorizationcomponents/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch

func (r *AuthorizationComponentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var component authv1.AuthorizationComponent
	if err := r.Get(ctx, req.NamespacedName, &component); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	var components authv1.AuthorizationComponentList
	if err := r.List(ctx, &components, client.InNamespace(req.Namespace)); err != nil {
		return ctrl.Result{}, err
	}

	authorizationModel, err := model.Compile(components.Items)
	if err != nil {
		if statusErr := r.patchStatus(ctx, req.NamespacedName, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "ModelCompileFailed",
			Message:            err.Error(),
			ObservedGeneration: component.Generation,
		}, ""); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{}, err
	}

	modelHash, err := model.Hash(authorizationModel)
	if err != nil {
		return ctrl.Result{}, err
	}

	modelJSON, err := model.MarshalWriteRequest(authorizationModel)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.applyAuthorizationModelConfigMap(ctx, req.Namespace, modelJSON, modelHash); err != nil {
		if statusErr := r.patchStatus(ctx, req.NamespacedName, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "ModelConfigMapApplyFailed",
			Message:            err.Error(),
			ObservedGeneration: component.Generation,
		}, ""); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{}, err
	}

	if err := r.patchStatus(ctx, req.NamespacedName, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "ModelCompiled",
		Message:            "Authorization model compiled",
		ObservedGeneration: component.Generation,
	}, modelHash); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Compiled authorization model", "authorizationComponentCount", len(components.Items), "modelHash", modelHash, "configMap", authorizationModelConfigMapName)

	return ctrl.Result{}, nil
}

func (r *AuthorizationComponentReconciler) applyAuthorizationModelConfigMap(ctx context.Context, namespace string, modelJSON []byte, modelHash string) error {
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

func (r *AuthorizationComponentReconciler) patchStatus(ctx context.Context, namespacedName types.NamespacedName, condition metav1.Condition, modelHash string) error {
	var component authv1.AuthorizationComponent
	if err := r.Get(ctx, namespacedName, &component); err != nil {
		return err
	}

	patch := client.MergeFrom(component.DeepCopy())
	component.Status.ObservedModelHash = modelHash
	setCondition(&component.Status.Conditions, condition)
	return r.Status().Patch(ctx, &component, patch)
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
func (r *AuthorizationComponentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&authv1.AuthorizationComponent{}).
		Named("authorizationcomponent").
		Complete(r)
}
