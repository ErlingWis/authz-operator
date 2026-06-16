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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	authv1 "my.domain/fga/api/v1"
)

var _ = Describe("AuthorizationModule Controller", func() {
	ctx := context.Background()

	BeforeEach(func() {
		ensureNamespace(ctx, defaultModelNamespace)
	})

	AfterEach(func() {
		deleteAuthorizationModules(ctx)
		deleteConfigMap(ctx, defaultModelNamespace, authorizationModelConfigMapName)
	})

	It("should reconcile one resource into the global model ConfigMap", func() {
		namespacedName := types.NamespacedName{Name: "test-resource", Namespace: "default"}
		resource := &authv1.AuthorizationModule{
			ObjectMeta: metav1.ObjectMeta{
				Name:      namespacedName.Name,
				Namespace: namespacedName.Namespace,
			},
			Spec: authv1.AuthorizationModuleSpec{
				Resource: "project",
				Roles: map[string]authv1.AuthorizationRole{
					"owner": {
						Subjects: []authv1.AuthorizationSubject{{Type: "user"}},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, resource)).To(Succeed())

		controllerReconciler := &AuthorizationModuleReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}

		_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())

		var reconciled authv1.AuthorizationModule
		Expect(k8sClient.Get(ctx, namespacedName, &reconciled)).To(Succeed())
		Expect(reconciled.Status.ObservedModelHash).NotTo(BeEmpty())
		Expect(reconciled.Status.Conditions).To(ContainElement(SatisfyAll(
			HaveField("Type", "Ready"),
			HaveField("Status", metav1.ConditionTrue),
			HaveField("Reason", "ModelCompiled"),
		)))

		var configMap corev1.ConfigMap
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name:      authorizationModelConfigMapName,
			Namespace: defaultModelNamespace,
		}, &configMap)).To(Succeed())
		Expect(configMap.Data[authorizationModelHashKey]).To(Equal(reconciled.Status.ObservedModelHash))
		Expect(json.Valid([]byte(configMap.Data[authorizationModelConfigMapKey]))).To(BeTrue())
	})

	It("should compile modules from multiple namespaces into one model", func() {
		ensureNamespace(ctx, "projects")
		ensureNamespace(ctx, "payments")

		projectModule := &authv1.AuthorizationModule{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "project-authorization",
				Namespace: "projects",
			},
			Spec: authv1.AuthorizationModuleSpec{
				Resource: "project",
				Roles: map[string]authv1.AuthorizationRole{
					"editor": {
						Subjects: []authv1.AuthorizationSubject{
							{Type: "user"},
						},
					},
					"owner": {
						Subjects: []authv1.AuthorizationSubject{{Type: "user"}},
					},
				},
				Permissions: map[string]authv1.AuthorizationPermission{
					"view": {
						AnyOf: []string{"owner", "editor"},
					},
				},
			},
		}
		paymentModule := &authv1.AuthorizationModule{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "payment-authorization",
				Namespace: "payments",
			},
			Spec: authv1.AuthorizationModuleSpec{
				Resource: "payment",
				Topology: map[string]authv1.TopologyRelation{
					"parent": {Resources: []string{"project"}},
				},
				Roles: map[string]authv1.AuthorizationRole{
					"payer": {
						Subjects: []authv1.AuthorizationSubject{
							{Type: "user"},
						},
					},
					"viewer": {
						Inherited: []authv1.InheritedRelation{
							{Via: "parent", Relation: "editor"},
						},
					},
				},
				Permissions: map[string]authv1.AuthorizationPermission{
					"refund": {
						AnyOf: []string{"payer"},
					},
				},
			},
		}

		Expect(k8sClient.Create(ctx, projectModule)).To(Succeed())
		Expect(k8sClient.Create(ctx, paymentModule)).To(Succeed())

		controllerReconciler := &AuthorizationModuleReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}

		_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "payment-authorization", Namespace: "payments"},
		})
		Expect(err).NotTo(HaveOccurred())

		var configMap corev1.ConfigMap
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name:      authorizationModelConfigMapName,
			Namespace: defaultModelNamespace,
		}, &configMap)).To(Succeed())

		model := decodedModel(configMap.Data[authorizationModelConfigMapKey])
		typeDefinitions := model["type_definitions"].([]any)
		Expect(typeDefinitionsByName(typeDefinitions)).To(HaveKey("project"))
		Expect(typeDefinitionsByName(typeDefinitions)).To(HaveKey("payment"))

		projectRelations := relationsForType(typeDefinitions, "project")
		Expect(projectRelations).To(HaveKey("owner"))
		Expect(projectRelations).To(HaveKey("editor"))

		paymentMetadata := metadataRelationsForType(typeDefinitions, "payment")
		Expect(paymentMetadata).To(HaveKey("payer"))
		Expect(paymentMetadata).To(HaveKey("parent"))
		payerTypes := paymentMetadata["payer"].(map[string]any)["directly_related_user_types"].([]any)
		Expect(payerTypes).To(ContainElement(SatisfyAll(
			HaveKeyWithValue("type", "user"),
		)))
		paymentRelations := relationsForType(typeDefinitions, "payment")
		viewerRelation := paymentRelations["viewer"].(map[string]any)
		Expect(viewerRelation["tupleToUserset"]).To(SatisfyAll(
			HaveKey("tupleset"),
			HaveKey("computedUserset"),
		))

		var reconciledProject authv1.AuthorizationModule
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "project-authorization", Namespace: "projects"}, &reconciledProject)).To(Succeed())
		var reconciledPayment authv1.AuthorizationModule
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "payment-authorization", Namespace: "payments"}, &reconciledPayment)).To(Succeed())
		Expect(reconciledProject.Status.ObservedModelHash).To(Equal(configMap.Data[authorizationModelHashKey]))
		Expect(reconciledPayment.Status.ObservedModelHash).To(Equal(configMap.Data[authorizationModelHashKey]))
	})
})

func ensureNamespace(ctx context.Context, name string) {
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	err := k8sClient.Create(ctx, namespace)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}

func deleteConfigMap(ctx context.Context, namespace, name string) {
	configMap := &corev1.ConfigMap{}
	err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, configMap)
	if apierrors.IsNotFound(err) {
		return
	}
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient.Delete(ctx, configMap)).To(Succeed())
}

func deleteAuthorizationModules(ctx context.Context) {
	var modules authv1.AuthorizationModuleList
	Expect(k8sClient.List(ctx, &modules)).To(Succeed())
	for i := range modules.Items {
		Expect(k8sClient.Delete(ctx, &modules.Items[i])).To(Succeed())
	}
}

func decodedModel(data string) map[string]any {
	var model map[string]any
	Expect(json.Unmarshal([]byte(data), &model)).To(Succeed())
	return model
}

func typeDefinitionsByName(typeDefinitions []any) map[string]map[string]any {
	byName := map[string]map[string]any{}
	for _, item := range typeDefinitions {
		typeDefinition := item.(map[string]any)
		byName[typeDefinition["type"].(string)] = typeDefinition
	}
	return byName
}

func relationsForType(typeDefinitions []any, name string) map[string]any {
	typeDefinition := typeDefinitionsByName(typeDefinitions)[name]
	return typeDefinition["relations"].(map[string]any)
}

func metadataRelationsForType(typeDefinitions []any, name string) map[string]any {
	typeDefinition := typeDefinitionsByName(typeDefinitions)[name]
	metadata := typeDefinition["metadata"].(map[string]any)
	return metadata["relations"].(map[string]any)
}
