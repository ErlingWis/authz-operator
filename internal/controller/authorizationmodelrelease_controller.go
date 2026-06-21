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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	authv1 "erli.ng/authz-operator/api/v1"
)

// AuthorizationModelReleaseReconciler promotes published candidate models to stable.
type AuthorizationModelReleaseReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=auth.bridder.io,resources=authorizationmodelreleases,verbs=get;list;watch
// +kubebuilder:rbac:groups=auth.bridder.io,resources=authorizationmodelreleases/status,verbs=get;update;patch

func (r *AuthorizationModelReleaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var release authv1.AuthorizationModelRelease
	if err := r.Get(ctx, req.NamespacedName, &release); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	stableModelHash := release.Spec.StableModelHash
	if stableModelHash == "" {
		return ctrl.Result{}, nil
	}

	patch := client.MergeFrom(release.DeepCopy())
	if release.Status.Candidate == nil {
		setCondition(&release.Status.Conditions, metav1.Condition{
			Type:               "StablePromoted",
			Status:             metav1.ConditionFalse,
			Reason:             "CandidateNotPublished",
			Message:            "Candidate authorization model is not published",
			ObservedGeneration: release.Generation,
		})
		if err := r.Status().Patch(ctx, &release, patch); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if release.Status.Candidate.ModelHash != stableModelHash {
		setCondition(&release.Status.Conditions, metav1.Condition{
			Type:               "StablePromoted",
			Status:             metav1.ConditionFalse,
			Reason:             "CandidateHashMismatch",
			Message:            "Requested stable model hash does not match the current candidate",
			ObservedGeneration: release.Generation,
		})
		if err := r.Status().Patch(ctx, &release, patch); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if release.Status.Stable != nil && release.Status.Stable.ModelHash == stableModelHash {
		return ctrl.Result{}, nil
	}

	candidate := *release.Status.Candidate
	release.Status.Stable = &candidate
	setCondition(&release.Status.Conditions, metav1.Condition{
		Type:               "StablePromoted",
		Status:             metav1.ConditionTrue,
		Reason:             "CandidatePromoted",
		Message:            "Candidate authorization model promoted to stable",
		ObservedGeneration: release.Generation,
	})
	if err := r.Status().Patch(ctx, &release, patch); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Promoted candidate authorization model to stable", "modelHash", stableModelHash, "openfgaStoreID", candidate.OpenFGAStoreID, "openfgaModelID", candidate.OpenFGAModelID)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AuthorizationModelReleaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&authv1.AuthorizationModelRelease{}).
		Named("authorizationmodelrelease").
		Complete(r)
}
