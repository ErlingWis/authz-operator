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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	authv1 "my.domain/fga/api/v1"
)

var _ = Describe("AuthorizationComponent Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		authorizationcomponent := &authv1.AuthorizationComponent{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind AuthorizationComponent")
			err := k8sClient.Get(ctx, typeNamespacedName, authorizationcomponent)
			if err != nil && errors.IsNotFound(err) {
				resource := &authv1.AuthorizationComponent{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: authv1.AuthorizationComponentSpec{
						Resource: "project",
						Roles: map[string]authv1.AuthorizationRole{
							"owner": {
								Subjects: []authv1.AuthorizationSubject{{Type: "user"}},
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &authv1.AuthorizationComponent{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance AuthorizationComponent")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &AuthorizationComponentReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			var reconciled authv1.AuthorizationComponent
			Expect(k8sClient.Get(ctx, typeNamespacedName, &reconciled)).To(Succeed())
			Expect(reconciled.Status.ObservedModelHash).NotTo(BeEmpty())
			Expect(reconciled.Status.Conditions).To(ContainElement(SatisfyAll(
				HaveField("Type", "Ready"),
				HaveField("Status", metav1.ConditionTrue),
				HaveField("Reason", "ModelCompiled"),
			)))

			var configMap corev1.ConfigMap
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      authorizationModelConfigMapName,
				Namespace: typeNamespacedName.Namespace,
			}, &configMap)).To(Succeed())
			Expect(configMap.Data[authorizationModelHashKey]).To(Equal(reconciled.Status.ObservedModelHash))
			Expect(json.Valid([]byte(configMap.Data[authorizationModelConfigMapKey]))).To(BeTrue())
		})
	})
})
