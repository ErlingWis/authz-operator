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
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	authv1 "my.domain/fga/api/v1"
)

// AuthorizationModelReleaseReconciler promotes published candidate models to stable.
type AuthorizationModelReleaseReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	authorizationModelProxyConfigMapName        = "bridder-authorization-model-proxy-config"
	authorizationModelProxyDeploymentName       = "bridder-authorization-model-proxy"
	authorizationModelProxyConfigKey            = "nginx.conf"
	authorizationModelProxyConfigHashAnnotation = "auth.bridder.io/nginx-config-hash"

	openFGAAuthorizationModelHeader = "Openfga-Authorization-Model-Id"
	openFGAProxyUpstream            = "http://bridder-openfga:8080"

	authZenDiscoveryPath      = "/.well-known/authzen-configuration"
	authZenEvaluationPath     = "/access/v1/evaluation"
	authZenEvaluationsPath    = "/access/v1/evaluations"
	authZenResourceSearchPath = "/access/v1/search/resource"
	authZenSubjectSearchPath  = "/access/v1/search/subject"
	authZenActionSearchPath   = "/access/v1/search/action"
)

// +kubebuilder:rbac:groups=auth.bridder.io,resources=authorizationmodelreleases,verbs=get;list;watch
// +kubebuilder:rbac:groups=auth.bridder.io,resources=authorizationmodelreleases/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;patch

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
		if err := r.syncAuthZenProxyConfig(ctx, &release); err != nil {
			return ctrl.Result{}, err
		}
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
		if err := r.syncAuthZenProxyConfig(ctx, &release); err != nil {
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
		if err := r.syncAuthZenProxyConfig(ctx, &release); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if release.Status.Stable != nil && release.Status.Stable.ModelHash == stableModelHash {
		if err := r.syncAuthZenProxyConfig(ctx, &release); err != nil {
			return ctrl.Result{}, err
		}
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
	if err := r.syncAuthZenProxyConfig(ctx, &release); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *AuthorizationModelReleaseReconciler) syncAuthZenProxyConfig(ctx context.Context, release *authv1.AuthorizationModelRelease) error {
	config := renderAuthZenProxyConfig(release.Status.Stable)
	configHash := fmt.Sprintf("%x", sha256.Sum256([]byte(config)))

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: release.Namespace,
			Name:      authorizationModelProxyConfigMapName,
		},
	}
	_, err := ctrl.CreateOrUpdate(ctx, r.Client, configMap, func() error {
		if configMap.Labels == nil {
			configMap.Labels = map[string]string{}
		}
		configMap.Labels["app.kubernetes.io/name"] = "bridder"
		configMap.Labels["app.kubernetes.io/component"] = "authorization-model-proxy"
		configMap.Labels["app.kubernetes.io/managed-by"] = "bridder-controller"
		if configMap.Data == nil {
			configMap.Data = map[string]string{}
		}
		configMap.Data[authorizationModelProxyConfigKey] = config
		return ctrl.SetControllerReference(release, configMap, r.Scheme)
	})
	if err != nil {
		return err
	}

	var deployment appsv1.Deployment
	err = r.Get(ctx, client.ObjectKey{Namespace: release.Namespace, Name: authorizationModelProxyDeploymentName}, &deployment)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	patch := client.MergeFrom(deployment.DeepCopy())
	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = map[string]string{}
	}
	if deployment.Spec.Template.Annotations[authorizationModelProxyConfigHashAnnotation] == configHash {
		return nil
	}
	deployment.Spec.Template.Annotations[authorizationModelProxyConfigHashAnnotation] = configHash
	return r.Patch(ctx, &deployment, patch)
}

func renderAuthZenProxyConfig(stable *authv1.AuthorizationModelReleaseState) string {
	if stable == nil {
		return renderUnavailableAuthZenProxyConfig()
	}

	return fmt.Sprintf(`events {}

http {
    server {
        listen 8080;

%s

%s

        location / {
            return 404;
        }
    }
}
`, authZenDiscoveryLocation(), authZenProxyLocations(stable))
}

func renderUnavailableAuthZenProxyConfig() string {
	return fmt.Sprintf(`events {}

http {
    server {
        listen 8080;

%s

%s

        location / {
            return 404;
        }
    }
}
`, authZenDiscoveryLocation(), authZenUnavailableLocations())
}

func authZenDiscoveryLocation() string {
	return fmt.Sprintf(`        location = %s {
            default_type application/json;
            return 200 '{"access_evaluation_endpoint":"%s","access_evaluations_endpoint":"%s","search_resource_endpoint":"%s","search_subject_endpoint":"%s","search_action_endpoint":"%s"}';
        }`, authZenDiscoveryPath, authZenEvaluationPath, authZenEvaluationsPath, authZenResourceSearchPath, authZenSubjectSearchPath, authZenActionSearchPath)
}

func authZenProxyLocations(stable *authv1.AuthorizationModelReleaseState) string {
	paths := []string{
		authZenEvaluationPath,
		authZenEvaluationsPath,
		authZenResourceSearchPath,
		authZenSubjectSearchPath,
		authZenActionSearchPath,
	}

	locations := make([]string, 0, len(paths))
	for _, path := range paths {
		locations = append(locations, fmt.Sprintf(`        location = %s {
            proxy_pass %s/stores/%s%s;
            proxy_set_header %s %s;
            proxy_set_header Host $host;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto $scheme;
        }`, path, openFGAProxyUpstream, nginxToken(stable.OpenFGAStoreID), path, openFGAAuthorizationModelHeader, nginxToken(stable.OpenFGAModelID)))
	}
	return strings.Join(locations, "\n\n")
}

func authZenUnavailableLocations() string {
	paths := []string{
		authZenEvaluationPath,
		authZenEvaluationsPath,
		authZenResourceSearchPath,
		authZenSubjectSearchPath,
		authZenActionSearchPath,
	}

	locations := make([]string, 0, len(paths))
	for _, path := range paths {
		locations = append(locations, fmt.Sprintf(`        location = %s {
            return 503;
        }`, path))
	}
	return strings.Join(locations, "\n\n")
}

func nginxToken(value string) string {
	return strings.ReplaceAll(value, `\`, `\\`)
}

// SetupWithManager sets up the controller with the Manager.
func (r *AuthorizationModelReleaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&authv1.AuthorizationModelRelease{}).
		Owns(&corev1.ConfigMap{}).
		Named("authorizationmodelrelease").
		Complete(r)
}
