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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("AuthorizationModelPublisher Controller", func() {
	ctx := context.Background()
	publishedAt := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

	BeforeEach(func() {
		ensureNamespace(ctx, defaultModelNamespace)
	})

	AfterEach(func() {
		deleteConfigMap(ctx, defaultModelNamespace, authorizationModelConfigMapName)
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
