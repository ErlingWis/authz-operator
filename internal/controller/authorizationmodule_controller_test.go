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
	"errors"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	openfga "github.com/openfga/go-sdk"
	openfgaclient "github.com/openfga/go-sdk/client"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	authv1 "erli.ng/authz-operator/api/v1"
	"erli.ng/authz-operator/internal/openfgaconfig"
)

const testOpenFGAAPIURL = "https://openfga.example.test"

var _ = Describe("AuthorizationModule Controller", func() {
	ctx := context.Background()
	publishedAt := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

	BeforeEach(func() {
		ensureNamespace(ctx, defaultModelNamespace)
		setEnv(openFGAAPIURLEnv, testOpenFGAAPIURL)
		unsetEnv(openFGAAPITokenEnv)
		unsetEnv(openFGAStoreIDEnv)
		unsetEnv(openFGAStoreNameEnv)
	})

	AfterEach(func() {
		deleteAuthorizationModules(ctx)
		deleteSecret(ctx, defaultModelNamespace, openFGAClientConfigSecretName)
	})

	It("should publish one resource and write OpenFGA client configuration to a Secret", func() {
		setEnv(openFGAAPITokenEnv, "token-1")
		namespacedName := types.NamespacedName{Name: "test-resource", Namespace: "default"}
		Expect(k8sClient.Create(ctx, authorizationModule(namespacedName.Name, namespacedName.Namespace, "project"))).To(Succeed())

		publisher := &fakeAuthorizationModelPublisher{
			published: PublishedAuthorizationModel{StoreID: "store-1", ModelID: "model-1"},
		}
		reconciler := moduleReconciler(publisher, publishedAt)

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())
		Expect(publisher.calls).To(Equal(1))
		Expect(publisher.lastStoreID).To(BeEmpty())
		Expect(publisher.lastStoreName).To(Equal(defaultOpenFGAStoreName))

		var reconciled authv1.AuthorizationModule
		Expect(k8sClient.Get(ctx, namespacedName, &reconciled)).To(Succeed())
		Expect(reconciled.Status.ObservedModelHash).NotTo(BeEmpty())
		condition := apimeta.FindStatusCondition(reconciled.Status.Conditions, "Ready")
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		Expect(condition.Reason).To(Equal("ModelPublished"))

		secret := getClientConfigSecret(ctx)
		Expect(secret.Type).To(Equal(corev1.SecretTypeOpaque))
		Expect(secret.Annotations).To(HaveKeyWithValue(openFGAClientConfigHashKey, reconciled.Status.ObservedModelHash))
		Expect(secret.Annotations).To(HaveKeyWithValue(openFGAClientConfigTimeKey, publishedAt.Format(time.RFC3339)))

		config := decodedOutputConfig(secret)
		Expect(config.ApiUrl).To(Equal(testOpenFGAAPIURL))
		Expect(config.StoreId).To(Equal("store-1"))
		Expect(config.AuthorizationModelId).To(Equal("model-1"))
		Expect(config.Credentials).NotTo(BeNil())
		Expect(config.Credentials.Config.ApiToken).To(Equal("token-1"))
	})

	It("should compile modules from multiple namespaces into one published model", func() {
		ensureNamespace(ctx, "projects")
		ensureNamespace(ctx, "payments")

		projectModule := authorizationModule("project-authorization", "projects", "project")
		projectModule.Spec.Roles = map[string]authv1.AuthorizationRole{
			"editor": {Subjects: []authv1.AuthorizationSubject{{Type: "user"}}},
			"owner":  {Subjects: []authv1.AuthorizationSubject{{Type: "user"}}},
		}
		projectModule.Spec.Permissions = map[string]authv1.AuthorizationPermission{
			"view": {AnyOf: []string{"owner", "editor"}},
		}
		paymentModule := authorizationModule("payment-authorization", "payments", "payment")
		paymentModule.Spec.Topology = map[string]authv1.TopologyRelation{
			"parent": {Resources: []string{"project"}},
		}
		paymentModule.Spec.Roles = map[string]authv1.AuthorizationRole{
			"payer": {
				Subjects: []authv1.AuthorizationSubject{{Type: "user"}},
			},
			"viewer": {
				Inherited: []authv1.InheritedRelation{{Via: "parent", Relation: "editor"}},
			},
		}
		paymentModule.Spec.Permissions = map[string]authv1.AuthorizationPermission{
			"refund": {AnyOf: []string{"payer"}},
		}

		Expect(k8sClient.Create(ctx, projectModule)).To(Succeed())
		Expect(k8sClient.Create(ctx, paymentModule)).To(Succeed())

		publisher := &fakeAuthorizationModelPublisher{
			published: PublishedAuthorizationModel{StoreID: "store-1", ModelID: "model-1"},
		}
		reconciler := moduleReconciler(publisher, publishedAt)

		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "payment-authorization", Namespace: "payments"},
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(typeDefinitionNames(publisher.lastRequest.TypeDefinitions)).To(ContainElements("project", "payment"))
		var reconciledProject authv1.AuthorizationModule
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "project-authorization", Namespace: "projects"}, &reconciledProject)).To(Succeed())
		var reconciledPayment authv1.AuthorizationModule
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "payment-authorization", Namespace: "payments"}, &reconciledPayment)).To(Succeed())
		Expect(reconciledProject.Status.ObservedModelHash).NotTo(BeEmpty())
		Expect(reconciledPayment.Status.ObservedModelHash).To(Equal(reconciledProject.Status.ObservedModelHash))
	})

	It("should skip publishing when the output Secret already has the current model hash", func() {
		namespacedName := types.NamespacedName{Name: "test-resource", Namespace: "default"}
		Expect(k8sClient.Create(ctx, authorizationModule(namespacedName.Name, namespacedName.Namespace, "project"))).To(Succeed())

		publisher := &fakeAuthorizationModelPublisher{
			published: PublishedAuthorizationModel{StoreID: "store-1", ModelID: "model-1"},
		}
		reconciler := moduleReconciler(publisher, publishedAt)

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())
		Expect(publisher.calls).To(Equal(1))

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())
		Expect(publisher.calls).To(Equal(1))
	})

	It("should reuse the store ID from an existing client configuration Secret", func() {
		Expect(k8sClient.Create(ctx, outputSecret("old-hash", "store-existing", "model-existing"))).To(Succeed())
		namespacedName := types.NamespacedName{Name: "test-resource", Namespace: "default"}
		Expect(k8sClient.Create(ctx, authorizationModule(namespacedName.Name, namespacedName.Namespace, "project"))).To(Succeed())

		publisher := &fakeAuthorizationModelPublisher{
			published: PublishedAuthorizationModel{StoreID: "store-existing", ModelID: "model-2"},
		}
		reconciler := moduleReconciler(publisher, publishedAt)

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())
		Expect(publisher.lastStoreID).To(Equal("store-existing"))
	})

	It("should prefer OPENFGA_STORE_ID over a stored client configuration store ID", func() {
		setEnv(openFGAStoreIDEnv, "store-env")
		Expect(k8sClient.Create(ctx, outputSecret("old-hash", "store-existing", "model-existing"))).To(Succeed())
		namespacedName := types.NamespacedName{Name: "test-resource", Namespace: "default"}
		Expect(k8sClient.Create(ctx, authorizationModule(namespacedName.Name, namespacedName.Namespace, "project"))).To(Succeed())

		publisher := &fakeAuthorizationModelPublisher{
			published: PublishedAuthorizationModel{StoreID: "store-env", ModelID: "model-2"},
		}
		reconciler := moduleReconciler(publisher, publishedAt)

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())
		Expect(publisher.lastStoreID).To(Equal("store-env"))
	})

	It("should record publish failures without replacing the previous Secret", func() {
		Expect(k8sClient.Create(ctx, outputSecret("previous-hash", "store-previous", "model-previous"))).To(Succeed())
		namespacedName := types.NamespacedName{Name: "test-resource", Namespace: "default"}
		Expect(k8sClient.Create(ctx, authorizationModule(namespacedName.Name, namespacedName.Namespace, "project"))).To(Succeed())

		reconciler := moduleReconciler(&fakeAuthorizationModelPublisher{err: errors.New("openfga unavailable")}, publishedAt)

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).To(MatchError("openfga unavailable"))

		var reconciled authv1.AuthorizationModule
		Expect(k8sClient.Get(ctx, namespacedName, &reconciled)).To(Succeed())
		Expect(reconciled.Status.ObservedModelHash).To(BeEmpty())
		condition := apimeta.FindStatusCondition(reconciled.Status.Conditions, "Ready")
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Reason).To(Equal("ModelPublishFailed"))

		config := decodedOutputConfig(getClientConfigSecret(ctx))
		Expect(config.StoreId).To(Equal("store-previous"))
		Expect(config.AuthorizationModelId).To(Equal("model-previous"))
	})

	It("should mark modules not ready when compilation fails", func() {
		first := authorizationModule("first-resource", "default", "project")
		second := authorizationModule("second-resource", "default", "project")
		Expect(k8sClient.Create(ctx, first)).To(Succeed())
		Expect(k8sClient.Create(ctx, second)).To(Succeed())

		publisher := &fakeAuthorizationModelPublisher{}
		reconciler := moduleReconciler(publisher, publishedAt)

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: first.Name, Namespace: first.Namespace}})
		Expect(err).To(HaveOccurred())
		Expect(publisher.calls).To(Equal(0))

		var reconciled authv1.AuthorizationModule
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: first.Name, Namespace: first.Namespace}, &reconciled)).To(Succeed())
		Expect(reconciled.Status.ObservedModelHash).To(BeEmpty())
		condition := apimeta.FindStatusCondition(reconciled.Status.Conditions, "Ready")
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Reason).To(Equal("ModelCompileFailed"))

		var secret corev1.Secret
		err = k8sClient.Get(ctx, types.NamespacedName{Name: openFGAClientConfigSecretName, Namespace: defaultModelNamespace}, &secret)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("should mark modules not ready when writing the client configuration Secret fails", func() {
		immutable := true
		secret := outputSecret("old-hash", "store-existing", "model-existing")
		secret.Immutable = &immutable
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		namespacedName := types.NamespacedName{Name: "test-resource", Namespace: "default"}
		Expect(k8sClient.Create(ctx, authorizationModule(namespacedName.Name, namespacedName.Namespace, "project"))).To(Succeed())

		publisher := &fakeAuthorizationModelPublisher{
			published: PublishedAuthorizationModel{StoreID: "store-existing", ModelID: "model-2"},
		}
		reconciler := moduleReconciler(publisher, publishedAt)

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).To(HaveOccurred())

		var reconciled authv1.AuthorizationModule
		Expect(k8sClient.Get(ctx, namespacedName, &reconciled)).To(Succeed())
		Expect(reconciled.Status.ObservedModelHash).To(BeEmpty())
		condition := apimeta.FindStatusCondition(reconciled.Status.Conditions, "Ready")
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Reason).To(Equal("ClientConfigWriteFailed"))

		config := decodedOutputConfig(getClientConfigSecret(ctx))
		Expect(config.AuthorizationModelId).To(Equal("model-existing"))
	})

	It("should delete the output Secret when no modules remain", func() {
		Expect(k8sClient.Create(ctx, outputSecret("old-hash", "store-existing", "model-existing"))).To(Succeed())

		reconciler := moduleReconciler(&fakeAuthorizationModelPublisher{}, publishedAt)
		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: openFGAClientConfigSecretName, Namespace: defaultModelNamespace},
		})
		Expect(err).NotTo(HaveOccurred())

		var secret corev1.Secret
		err = k8sClient.Get(ctx, types.NamespacedName{Name: openFGAClientConfigSecretName, Namespace: defaultModelNamespace}, &secret)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})
})

type fakeAuthorizationModelPublisher struct {
	published     PublishedAuthorizationModel
	err           error
	calls         int
	lastStoreID   string
	lastStoreName string
	lastRequest   openfga.WriteAuthorizationModelRequest
}

func (p *fakeAuthorizationModelPublisher) Publish(_ context.Context, storeID, storeName string, request openfga.WriteAuthorizationModelRequest) (PublishedAuthorizationModel, error) {
	p.calls++
	p.lastStoreID = storeID
	p.lastStoreName = storeName
	p.lastRequest = request
	if p.err != nil {
		return PublishedAuthorizationModel{}, p.err
	}
	return p.published, nil
}

func moduleReconciler(publisher AuthorizationModelPublisher, now time.Time) *AuthorizationModuleReconciler {
	return &AuthorizationModuleReconciler{
		Client:    k8sClient,
		Scheme:    k8sClient.Scheme(),
		Publisher: publisher,
		Now: func() time.Time {
			return now
		},
	}
}

func authorizationModule(name, namespace, resource string) *authv1.AuthorizationModule {
	return &authv1.AuthorizationModule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: authv1.AuthorizationModuleSpec{
			Resource: resource,
			Roles: map[string]authv1.AuthorizationRole{
				"owner": {
					Subjects: []authv1.AuthorizationSubject{{Type: "user"}},
				},
			},
		},
	}
}

func outputSecret(modelHash, storeID, modelID string) *corev1.Secret {
	data, err := openfgaconfig.Marshal(openfgaconfig.New(testOpenFGAAPIURL, "", storeID, modelID))
	Expect(err).NotTo(HaveOccurred())
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      openFGAClientConfigSecretName,
			Namespace: defaultModelNamespace,
			Annotations: map[string]string{
				openFGAClientConfigHashKey: modelHash,
				openFGAClientConfigTimeKey: time.Date(2026, 6, 14, 11, 0, 0, 0, time.UTC).Format(time.RFC3339),
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			openFGAClientConfigKey: data,
		},
	}
}

func getClientConfigSecret(ctx context.Context) corev1.Secret {
	var secret corev1.Secret
	Expect(k8sClient.Get(ctx, types.NamespacedName{
		Name:      openFGAClientConfigSecretName,
		Namespace: defaultModelNamespace,
	}, &secret)).To(Succeed())
	return secret
}

func decodedOutputConfig(secret corev1.Secret) openfgaclient.ClientConfiguration {
	var config openfgaclient.ClientConfiguration
	Expect(json.Unmarshal(secret.Data[openFGAClientConfigKey], &config)).To(Succeed())
	return config
}

func typeDefinitionNames(typeDefinitions []openfga.TypeDefinition) []string {
	names := make([]string, 0, len(typeDefinitions))
	for _, typeDefinition := range typeDefinitions {
		names = append(names, typeDefinition.Type)
	}
	return names
}

func ensureNamespace(ctx context.Context, name string) {
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	err := k8sClient.Create(ctx, namespace)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}

func deleteSecret(ctx context.Context, namespace, name string) {
	secret := &corev1.Secret{}
	err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, secret)
	if apierrors.IsNotFound(err) {
		return
	}
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
}

func deleteAuthorizationModules(ctx context.Context) {
	var modules authv1.AuthorizationModuleList
	Expect(k8sClient.List(ctx, &modules)).To(Succeed())
	for i := range modules.Items {
		Expect(k8sClient.Delete(ctx, &modules.Items[i])).To(Succeed())
	}
}

func setEnv(name, value string) {
	previous, found := os.LookupEnv(name)
	Expect(os.Setenv(name, value)).To(Succeed())
	DeferCleanup(func() {
		if found {
			Expect(os.Setenv(name, previous)).To(Succeed())
			return
		}
		Expect(os.Unsetenv(name)).To(Succeed())
	})
}

func unsetEnv(name string) {
	previous, found := os.LookupEnv(name)
	Expect(os.Unsetenv(name)).To(Succeed())
	DeferCleanup(func() {
		if found {
			Expect(os.Setenv(name, previous)).To(Succeed())
			return
		}
		Expect(os.Unsetenv(name)).To(Succeed())
	})
}
