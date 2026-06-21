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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	authv1 "erli.ng/authz-operator/api/v1"
)

var _ = Describe("AuthorizationModelRelease Controller", func() {
	ctx := context.Background()
	publishedAt := metav1.NewTime(time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC))

	BeforeEach(func() {
		ensureNamespace(ctx, defaultModelNamespace)
	})

	AfterEach(func() {
		deleteAuthorizationModelRelease(ctx, defaultModelNamespace, authorizationModelReleaseName)
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

func deleteAuthorizationModelRelease(ctx context.Context, namespace, name string) {
	var release authv1.AuthorizationModelRelease
	err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &release)
	if err == nil {
		Expect(k8sClient.Delete(ctx, &release)).To(Succeed())
		return
	}
	Expect(apierrors.IsNotFound(err)).To(BeTrue())
}
