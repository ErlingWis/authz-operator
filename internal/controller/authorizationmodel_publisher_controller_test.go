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
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	openfga "github.com/openfga/go-sdk"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	authv1 "erli.ng/authz-operator/api/v1"
)

var _ = Describe("AuthorizationModelPublisher Controller", func() {
	ctx := context.Background()
	publishedAt := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

	BeforeEach(func() {
		ensureNamespace(ctx, defaultModelNamespace)
	})

	AfterEach(func() {
		deleteConfigMap(ctx, defaultModelNamespace, authorizationModelConfigMapName)
		deleteAuthorizationModelRelease(ctx, defaultModelNamespace, authorizationModelReleaseName)
	})

	It("should publish a new candidate model and annotate the ConfigMap", func() {
		configMap := authorizationModelConfigMap()
		Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

		publisher := &fakeAuthorizationModelPublisher{
			published: PublishedAuthorizationModel{StoreID: "store-1", ModelID: "model-1"},
		}
		reconciler := publisherReconciler(publisher, publishedAt)

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: authorizationModelConfigMapNamespacedName()})
		Expect(err).NotTo(HaveOccurred())
		Expect(publisher.calls).To(Equal(1))
		Expect(publisher.lastStoreName).To(Equal(defaultOpenFGAStoreName))

		var reconciled corev1.ConfigMap
		Expect(k8sClient.Get(ctx, authorizationModelConfigMapNamespacedName(), &reconciled)).To(Succeed())
		Expect(reconciled.Annotations).To(HaveKeyWithValue(annotationCandidateModelHash, "hash-1"))
		Expect(reconciled.Annotations).To(HaveKeyWithValue(annotationCandidateOpenFGAModelID, "model-1"))
		Expect(reconciled.Annotations).To(HaveKeyWithValue(annotationCandidateOpenFGAStoreID, "store-1"))
		Expect(reconciled.Annotations).To(HaveKeyWithValue(annotationCandidatePublishedAt, publishedAt.Format(time.RFC3339)))

		var release authv1.AuthorizationModelRelease
		Expect(k8sClient.Get(ctx, authorizationModelReleaseNamespacedName(), &release)).To(Succeed())
		Expect(release.Status.Candidate).NotTo(BeNil())
		Expect(release.Status.Candidate.ModelHash).To(Equal("hash-1"))
		Expect(release.Status.Candidate.OpenFGAModelID).To(Equal("model-1"))
		Expect(release.Status.Candidate.OpenFGAStoreID).To(Equal("store-1"))
		Expect(release.Status.Candidate.PublishedAt.Time).To(BeTemporally("==", publishedAt))
		Expect(release.Status.Stable).To(BeNil())
		condition := apimeta.FindStatusCondition(release.Status.Conditions, "CandidatePublished")
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		Expect(condition.Reason).To(Equal("OpenFGAModelWritten"))
	})

	It("should preserve stable release state when publishing a new candidate", func() {
		release := &authv1.AuthorizationModelRelease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      authorizationModelReleaseName,
				Namespace: defaultModelNamespace,
			},
			Spec: authv1.AuthorizationModelReleaseSpec{},
		}
		Expect(k8sClient.Create(ctx, release)).To(Succeed())
		Expect(k8sClient.Get(ctx, authorizationModelReleaseNamespacedName(), release)).To(Succeed())
		patch := client.MergeFrom(release.DeepCopy())
		release.Status.Stable = &authv1.AuthorizationModelReleaseState{
			ModelHash:      "stable-hash",
			OpenFGAStoreID: "store-1",
			OpenFGAModelID: "stable-model",
			PublishedAt:    metav1.NewTime(publishedAt.Add(-time.Hour)),
		}
		Expect(k8sClient.Status().Patch(ctx, release, patch)).To(Succeed())

		configMap := authorizationModelConfigMap()
		Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

		publisher := &fakeAuthorizationModelPublisher{
			published: PublishedAuthorizationModel{StoreID: "store-1", ModelID: "candidate-model"},
		}
		reconciler := publisherReconciler(publisher, publishedAt)

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: authorizationModelConfigMapNamespacedName()})
		Expect(err).NotTo(HaveOccurred())

		var reconciled authv1.AuthorizationModelRelease
		Expect(k8sClient.Get(ctx, authorizationModelReleaseNamespacedName(), &reconciled)).To(Succeed())
		Expect(reconciled.Status.Stable).NotTo(BeNil())
		Expect(reconciled.Status.Stable.ModelHash).To(Equal("stable-hash"))
		Expect(reconciled.Status.Candidate).NotTo(BeNil())
		Expect(reconciled.Status.Candidate.ModelHash).To(Equal("hash-1"))
		Expect(reconciled.Status.Candidate.OpenFGAModelID).To(Equal("candidate-model"))
	})

	It("should skip publishing when the current hash is already the candidate", func() {
		configMap := authorizationModelConfigMap()
		configMap.Annotations = map[string]string{annotationCandidateModelHash: "hash-1"}
		Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

		publisher := &fakeAuthorizationModelPublisher{}
		reconciler := publisherReconciler(publisher, publishedAt)

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: authorizationModelConfigMapNamespacedName()})
		Expect(err).NotTo(HaveOccurred())
		Expect(publisher.calls).To(Equal(0))
	})

	It("should record publish errors on the ConfigMap", func() {
		configMap := authorizationModelConfigMap()
		Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

		publisher := &fakeAuthorizationModelPublisher{err: errors.New("openfga unavailable")}
		reconciler := publisherReconciler(publisher, publishedAt)

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: authorizationModelConfigMapNamespacedName()})
		Expect(err).NotTo(HaveOccurred())

		var reconciled corev1.ConfigMap
		Expect(k8sClient.Get(ctx, authorizationModelConfigMapNamespacedName(), &reconciled)).To(Succeed())
		Expect(reconciled.Annotations).To(HaveKeyWithValue(annotationPublishError, "openfga unavailable"))
		Expect(reconciled.Annotations).To(HaveKeyWithValue(annotationPublishErrorAt, publishedAt.Format(time.RFC3339)))
	})

	It("should ignore unrelated ConfigMaps", func() {
		ensureNamespace(ctx, "other")
		configMap := authorizationModelConfigMap()
		configMap.Namespace = "other"
		Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

		publisher := &fakeAuthorizationModelPublisher{}
		reconciler := publisherReconciler(publisher, publishedAt)

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: "other",
			Name:      authorizationModelConfigMapName,
		}})
		Expect(err).NotTo(HaveOccurred())
		Expect(publisher.calls).To(Equal(0))
	})

})

type fakeAuthorizationModelPublisher struct {
	published     PublishedAuthorizationModel
	err           error
	calls         int
	lastStoreID   string
	lastStoreName string
}

func (p *fakeAuthorizationModelPublisher) PublishCandidate(_ context.Context, storeID, storeName string, _ openfga.WriteAuthorizationModelRequest) (PublishedAuthorizationModel, error) {
	p.calls++
	p.lastStoreID = storeID
	p.lastStoreName = storeName
	if p.err != nil {
		return PublishedAuthorizationModel{}, p.err
	}
	return p.published, nil
}

func publisherReconciler(publisher AuthorizationModelPublisher, now time.Time) *AuthorizationModelPublisherReconciler {
	return &AuthorizationModelPublisherReconciler{
		Client:    k8sClient,
		Scheme:    k8sClient.Scheme(),
		Publisher: publisher,
		Now: func() time.Time {
			return now
		},
	}
}

func authorizationModelConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      authorizationModelConfigMapName,
			Namespace: defaultModelNamespace,
		},
		Data: map[string]string{
			authorizationModelHashKey:      "hash-1",
			authorizationModelConfigMapKey: validAuthorizationModelJSON(),
		},
	}
}

func authorizationModelConfigMapNamespacedName() types.NamespacedName {
	return types.NamespacedName{
		Namespace: defaultModelNamespace,
		Name:      authorizationModelConfigMapName,
	}
}

func authorizationModelReleaseNamespacedName() types.NamespacedName {
	return types.NamespacedName{
		Namespace: defaultModelNamespace,
		Name:      authorizationModelReleaseName,
	}
}

func validAuthorizationModelJSON() string {
	return `{
  "schema_version": "1.1",
  "type_definitions": [
    {
      "type": "user"
    }
  ]
}`
}
