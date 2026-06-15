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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	authv1 "my.domain/fga/api/v1"
)

var _ = Describe("AuthorizationModelRelease Controller", func() {
	ctx := context.Background()
	publishedAt := metav1.NewTime(time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC))

	BeforeEach(func() {
		ensureNamespace(ctx, defaultModelNamespace)
	})

	AfterEach(func() {
		deleteAuthorizationModelRelease(ctx, defaultModelNamespace, authorizationModelReleaseName)
		deleteConfigMap(ctx, defaultModelNamespace, authorizationModelProxyConfigMapName)
		deleteDeployment(ctx, defaultModelNamespace, authorizationModelProxyDeploymentName)
	})

	It("should promote the matching candidate to stable without changing candidate", func() {
		release := authorizationModelReleaseWithCandidate("hash-1", "model-1", publishedAt)
		release.Spec.StableModelHash = "hash-1"
		Expect(k8sClient.Create(ctx, release)).To(Succeed())
		patchReleaseStatus(ctx, release, func(release *authv1.AuthorizationModelRelease) {
			release.Status.Candidate = &authv1.AuthorizationModelReleaseState{
				ModelHash:      "hash-1",
				OpenFGAStoreID: "store-1",
				OpenFGAModelID: "model-1",
				PublishedAt:    publishedAt,
			}
		})

		reconciler := releaseReconciler()
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: authorizationModelReleaseNamespacedName()})
		Expect(err).NotTo(HaveOccurred())

		var reconciled authv1.AuthorizationModelRelease
		Expect(k8sClient.Get(ctx, authorizationModelReleaseNamespacedName(), &reconciled)).To(Succeed())
		Expect(reconciled.Status.Candidate).NotTo(BeNil())
		Expect(reconciled.Status.Candidate.ModelHash).To(Equal("hash-1"))
		Expect(reconciled.Status.Stable).NotTo(BeNil())
		Expect(reconciled.Status.Stable.ModelHash).To(Equal("hash-1"))
		Expect(reconciled.Status.Stable.OpenFGAModelID).To(Equal("model-1"))

		condition := apimeta.FindStatusCondition(reconciled.Status.Conditions, "StablePromoted")
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		Expect(condition.Reason).To(Equal("CandidatePromoted"))
	})

	It("should render nginx proxy config from the stable model and roll the proxy deployment", func() {
		release := authorizationModelReleaseWithCandidate("hash-1", "model-1", publishedAt)
		release.Spec.StableModelHash = "hash-1"
		Expect(k8sClient.Create(ctx, release)).To(Succeed())
		patchReleaseStatus(ctx, release, func(release *authv1.AuthorizationModelRelease) {
			release.Status.Candidate = &authv1.AuthorizationModelReleaseState{
				ModelHash:      "hash-1",
				OpenFGAStoreID: "store-1",
				OpenFGAModelID: "model-1",
				PublishedAt:    publishedAt,
			}
		})
		Expect(k8sClient.Create(ctx, authorizationModelProxyDeployment())).To(Succeed())

		reconciler := releaseReconciler()
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: authorizationModelReleaseNamespacedName()})
		Expect(err).NotTo(HaveOccurred())

		var configMap corev1.ConfigMap
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: defaultModelNamespace, Name: authorizationModelProxyConfigMapName}, &configMap)).To(Succeed())
		nginxConfig := configMap.Data[authorizationModelProxyConfigKey]
		Expect(nginxConfig).To(ContainSubstring("proxy_pass http://bridder-openfga:8080/stores/store-1/access/v1/evaluation;"))
		Expect(nginxConfig).To(ContainSubstring("proxy_pass http://bridder-openfga:8080/stores/store-1/access/v1/evaluations;"))
		Expect(nginxConfig).To(ContainSubstring("proxy_pass http://bridder-openfga:8080/stores/store-1/access/v1/search/resource;"))
		Expect(nginxConfig).To(ContainSubstring("proxy_pass http://bridder-openfga:8080/stores/store-1/access/v1/search/subject;"))
		Expect(nginxConfig).To(ContainSubstring("proxy_pass http://bridder-openfga:8080/stores/store-1/access/v1/search/action;"))
		Expect(nginxConfig).To(ContainSubstring("proxy_set_header Openfga-Authorization-Model-Id model-1;"))
		Expect(nginxConfig).To(ContainSubstring("location = /.well-known/authzen-configuration"))
		Expect(nginxConfig).To(ContainSubstring(`"access_evaluations_endpoint":"/access/v1/evaluations"`))

		var deployment appsv1.Deployment
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: defaultModelNamespace, Name: authorizationModelProxyDeploymentName}, &deployment)).To(Succeed())
		configHash := fmt.Sprintf("%x", sha256.Sum256([]byte(nginxConfig)))
		Expect(deployment.Spec.Template.Annotations).To(HaveKeyWithValue(authorizationModelProxyConfigHashAnnotation, configHash))
	})

	It("should render unavailable nginx proxy config before a stable model exists", func() {
		release := &authv1.AuthorizationModelRelease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      authorizationModelReleaseName,
				Namespace: defaultModelNamespace,
			},
			Spec: authv1.AuthorizationModelReleaseSpec{},
		}
		Expect(k8sClient.Create(ctx, release)).To(Succeed())

		reconciler := releaseReconciler()
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: authorizationModelReleaseNamespacedName()})
		Expect(err).NotTo(HaveOccurred())

		var configMap corev1.ConfigMap
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: defaultModelNamespace, Name: authorizationModelProxyConfigMapName}, &configMap)).To(Succeed())
		nginxConfig := configMap.Data[authorizationModelProxyConfigKey]
		Expect(nginxConfig).To(ContainSubstring("location = /.well-known/authzen-configuration"))
		Expect(nginxConfig).To(ContainSubstring("location = /access/v1/evaluation"))
		Expect(nginxConfig).To(ContainSubstring("location = /access/v1/evaluations"))
		Expect(nginxConfig).To(ContainSubstring("location = /access/v1/search/resource"))
		Expect(nginxConfig).To(ContainSubstring("location = /access/v1/search/subject"))
		Expect(nginxConfig).To(ContainSubstring("location = /access/v1/search/action"))
		Expect(nginxConfig).To(ContainSubstring("return 503;"))
		Expect(nginxConfig).NotTo(ContainSubstring(openFGAAuthorizationModelHeader))
	})

	It("should reject promotion when the requested stable hash is not the current candidate", func() {
		release := authorizationModelReleaseWithCandidate("hash-1", "model-1", publishedAt)
		release.Spec.StableModelHash = "other-hash"
		Expect(k8sClient.Create(ctx, release)).To(Succeed())
		patchReleaseStatus(ctx, release, func(release *authv1.AuthorizationModelRelease) {
			release.Status.Candidate = &authv1.AuthorizationModelReleaseState{
				ModelHash:      "hash-1",
				OpenFGAStoreID: "store-1",
				OpenFGAModelID: "model-1",
				PublishedAt:    publishedAt,
			}
		})

		reconciler := releaseReconciler()
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: authorizationModelReleaseNamespacedName()})
		Expect(err).NotTo(HaveOccurred())

		var reconciled authv1.AuthorizationModelRelease
		Expect(k8sClient.Get(ctx, authorizationModelReleaseNamespacedName(), &reconciled)).To(Succeed())
		Expect(reconciled.Status.Stable).To(BeNil())
		condition := apimeta.FindStatusCondition(reconciled.Status.Conditions, "StablePromoted")
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Reason).To(Equal("CandidateHashMismatch"))
	})

	It("should reject promotion when no candidate has been published", func() {
		release := &authv1.AuthorizationModelRelease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      authorizationModelReleaseName,
				Namespace: defaultModelNamespace,
			},
			Spec: authv1.AuthorizationModelReleaseSpec{StableModelHash: "hash-1"},
		}
		Expect(k8sClient.Create(ctx, release)).To(Succeed())

		reconciler := releaseReconciler()
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: authorizationModelReleaseNamespacedName()})
		Expect(err).NotTo(HaveOccurred())

		var reconciled authv1.AuthorizationModelRelease
		Expect(k8sClient.Get(ctx, authorizationModelReleaseNamespacedName(), &reconciled)).To(Succeed())
		Expect(reconciled.Status.Stable).To(BeNil())
		condition := apimeta.FindStatusCondition(reconciled.Status.Conditions, "StablePromoted")
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Reason).To(Equal("CandidateNotPublished"))
	})

	It("should ignore deleted releases", func() {
		reconciler := releaseReconciler()
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: authorizationModelReleaseNamespacedName()})
		Expect(err).NotTo(HaveOccurred())
	})
})

func releaseReconciler() *AuthorizationModelReleaseReconciler {
	return &AuthorizationModelReleaseReconciler{
		Client: k8sClient,
		Scheme: k8sClient.Scheme(),
	}
}

func authorizationModelReleaseWithCandidate(modelHash, modelID string, publishedAt metav1.Time) *authv1.AuthorizationModelRelease {
	return &authv1.AuthorizationModelRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      authorizationModelReleaseName,
			Namespace: defaultModelNamespace,
		},
		Spec: authv1.AuthorizationModelReleaseSpec{},
		Status: authv1.AuthorizationModelReleaseStatus{
			Candidate: &authv1.AuthorizationModelReleaseState{
				ModelHash:      modelHash,
				OpenFGAStoreID: "store-1",
				OpenFGAModelID: modelID,
				PublishedAt:    publishedAt,
			},
		},
	}
}

func patchReleaseStatus(ctx context.Context, release *authv1.AuthorizationModelRelease, update func(*authv1.AuthorizationModelRelease)) {
	Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: release.Namespace, Name: release.Name}, release)).To(Succeed())
	patch := client.MergeFrom(release.DeepCopy())
	update(release)
	Expect(k8sClient.Status().Patch(ctx, release, patch)).To(Succeed())
}

func authorizationModelProxyDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      authorizationModelProxyDeploymentName,
			Namespace: defaultModelNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app.kubernetes.io/name": "bridder",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app.kubernetes.io/name": "bridder",
					},
					Annotations: map[string]string{
						authorizationModelProxyConfigHashAnnotation: "old-hash",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "nginx",
						Image: "nginxinc/nginx-unprivileged:1.27-alpine",
					}},
				},
			},
		},
	}
}

func deleteAuthorizationModelRelease(ctx context.Context, namespace, name string) {
	var release authv1.AuthorizationModelRelease
	err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &release)
	if err == nil {
		Expect(k8sClient.Delete(ctx, &release)).To(Succeed())
		return
	}
	Expect(apierrors.IsNotFound(err)).To(BeTrue())
}

func deleteDeployment(ctx context.Context, namespace, name string) {
	var deployment appsv1.Deployment
	err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &deployment)
	if err == nil {
		Expect(k8sClient.Delete(ctx, &deployment)).To(Succeed())
		return
	}
	Expect(apierrors.IsNotFound(err)).To(BeTrue())
}
