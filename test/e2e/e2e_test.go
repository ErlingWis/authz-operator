//go:build e2e
// +build e2e

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

package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	openfga "github.com/openfga/go-sdk"
	openfgaclient "github.com/openfga/go-sdk/client"

	"erli.ng/authz-operator/test/utils"
)

// namespace where the project is deployed in
const namespace = "authz-operator-system"

// serviceAccountName created for the project
const serviceAccountName = "authz-operator-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "authz-operator-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "authz-operator-metrics-binding"

const fixtureNamespace = "authz-e2e"

const openFGADeploymentName = "authz-operator-openfga"
const openFGAServiceName = "authz-operator-openfga"

const openFGAClientConfigSecretName = "authz-operator-openfga-client-config"
const openFGAClientConfigKey = "client-configuration.json"
const openFGAClientConfigHashKey = "authz.erli.ng/model-hash"
const openFGAClientConfigTimeKey = "authz.erli.ng/published-at"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		By("removing any previous manager namespace")
		cmd := exec.Command("kubectl", "delete", "ns", namespace, "--ignore-not-found=true", "--wait=true")
		_, _ = utils.Run(cmd)

		By("creating manager namespace")
		cmd = exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("deploying OpenFGA and the Authz Operator config secret")
		cmd = exec.Command("kubectl", "apply", "-k", "config/dev/extras")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy OpenFGA and config secret")

		By("waiting for OpenFGA to become available")
		cmd = exec.Command("kubectl", "-n", namespace, "wait",
			fmt.Sprintf("deployment/%s", openFGADeploymentName),
			"--for=condition=Available",
			"--timeout=5m",
		)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed waiting for OpenFGA")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", managerImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")
	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		By("cleaning up the curl pod for metrics")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace)
		_, _ = utils.Run(cmd)

		By("cleaning up e2e fixture namespace")
		cmd = exec.Command("kubectl", "delete", "ns", fixtureNamespace, "--ignore-not-found=true")
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("deleting OpenFGA and Authz Operator config secret")
		cmd = exec.Command("kubectl", "delete", "-k", "config/dev/extras", "--ignore-not-found=true")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
			metricsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}

			By("Fetching controller manager pod description")
			cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				fmt.Println("Pod description:\n", podDescription)
			} else {
				fmt.Println("Failed to describe controller pod")
			}

			By("Fetching OpenFGA logs")
			cmd = exec.Command("kubectl", "logs", fmt.Sprintf("deployment/%s", openFGADeploymentName), "-n", namespace)
			openFGALogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "OpenFGA logs:\n %s", openFGALogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get OpenFGA logs: %s", err)
			}

			By("Fetching OpenFGA client configuration Secret")
			cmd = exec.Command("kubectl", "get", "secret", openFGAClientConfigSecretName, "-n", namespace, "-o", "json")
			secretOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "OpenFGA client configuration Secret:\n%s", secretOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get OpenFGA client configuration Secret: %s", err)
			}

			By("Fetching AuthorizationModules")
			cmd = exec.Command("kubectl", "get", "authorizationmodules", "-A", "-o", "yaml")
			modulesOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "AuthorizationModules:\n%s", modulesOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get AuthorizationModules: %s", err)
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				By("getting the name of the controller-manager pod")
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				By("validating the pod's status")
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		It("should publish AuthorizationModules to OpenFGA and recover from an invalid module", func() {
			ctx := context.Background()

			By("creating the e2e fixture namespace")
			Expect(applyYAML(fixtureNamespaceYAML())).To(Succeed())

			By("applying a focused document authorization module")
			Expect(applyYAML(documentAuthorizationModuleYAML())).To(Succeed())

			By("waiting for the document authorization module to be published")
			waitAuthorizationModuleReady(fixtureNamespace, "document-authorization", "True", "ModelPublished")

			By("reading the generated OpenFGA client configuration Secret")
			publishedSecret := waitOpenFGAClientConfigSecret()
			Expect(publishedSecret.Annotations).To(HaveKey(openFGAClientConfigHashKey))
			Expect(publishedSecret.Annotations[openFGAClientConfigHashKey]).NotTo(BeEmpty())
			Expect(publishedSecret.Annotations).To(HaveKey(openFGAClientConfigTimeKey))
			Expect(publishedSecret.Annotations[openFGAClientConfigTimeKey]).NotTo(BeEmpty())
			Expect(publishedSecret.Config.ApiUrl).To(Equal(fmt.Sprintf("http://%s:8080", openFGAServiceName)))
			Expect(publishedSecret.Config.StoreId).NotTo(BeEmpty())
			Expect(publishedSecret.Config.AuthorizationModelId).NotTo(BeEmpty())

			By("connecting to OpenFGA through a local port-forward")
			portForwardURL := startOpenFGAPortForward()
			clientConfig := publishedSecret.Config
			clientConfig.ApiUrl = portForwardURL
			openFGAClient, err := openfgaclient.NewSdkClient(&clientConfig)
			Expect(err).NotTo(HaveOccurred())

			By("reading the uploaded authorization model from OpenFGA")
			modelResponse, err := openFGAClient.ReadAuthorizationModel(ctx).Execute()
			Expect(err).NotTo(HaveOccurred())
			authorizationModel, ok := modelResponse.GetAuthorizationModelOk()
			Expect(ok).To(BeTrue())
			Expect(typeHasRelations(*authorizationModel, "document", "viewer", "can_view")).To(BeTrue())

			By("writing test tuples and checking authorization decisions")
			_, err = openFGAClient.Write(ctx).
				Options(openfgaclient.ClientWriteOptions{
					Conflict: openfgaclient.ClientWriteConflictOptions{
						OnDuplicateWrites: openfgaclient.CLIENT_WRITE_REQUEST_ON_DUPLICATE_WRITES_IGNORE,
					},
				}).
				Body(openfgaclient.ClientWriteRequest{
					Writes: []openfgaclient.ClientTupleKey{{
						User:     "user:alice",
						Relation: "viewer",
						Object:   "document:e2e-doc",
					}},
				}).
				Execute()
			Expect(err).NotTo(HaveOccurred())
			expectCheckAllowed(ctx, openFGAClient, "user:alice", "can_view", "document:e2e-doc", true)
			expectCheckAllowed(ctx, openFGAClient, "user:bob", "can_view", "document:e2e-doc", false)

			By("applying an invalid duplicate document module")
			Expect(applyYAML(duplicateDocumentAuthorizationModuleYAML())).To(Succeed())
			waitAuthorizationModuleReady(fixtureNamespace, "document-authorization", "False", "ModelCompileFailed")
			waitAuthorizationModuleReady(fixtureNamespace, "duplicate-document-authorization", "False", "ModelCompileFailed")

			By("verifying the last published Secret was not replaced")
			failedSecret := waitOpenFGAClientConfigSecret()
			Expect(failedSecret.Annotations[openFGAClientConfigHashKey]).To(Equal(publishedSecret.Annotations[openFGAClientConfigHashKey]))
			Expect(failedSecret.Config.StoreId).To(Equal(publishedSecret.Config.StoreId))
			Expect(failedSecret.Config.AuthorizationModelId).To(Equal(publishedSecret.Config.AuthorizationModelId))

			By("deleting the invalid module and waiting for recovery")
			cmd := exec.Command("kubectl", "-n", fixtureNamespace, "delete", "authorizationmodule", "duplicate-document-authorization")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			waitAuthorizationModuleReady(fixtureNamespace, "document-authorization", "True", "ModelPublished")

			By("verifying recovery keeps the published model available")
			recoveredSecret := waitOpenFGAClientConfigSecret()
			Expect(recoveredSecret.Annotations[openFGAClientConfigHashKey]).To(Equal(publishedSecret.Annotations[openFGAClientConfigHashKey]))
			Expect(recoveredSecret.Config.AuthorizationModelId).To(Equal(publishedSecret.Config.AuthorizationModelId))
		})

		It("should ensure the metrics endpoint is serving metrics", func() {
			By("creating a ClusterRoleBinding for the service account to allow access to metrics")
			cmd := exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
				"--clusterrole=authz-operator-metrics-reader",
				fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
			)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")

			By("validating that the metrics service is available")
			cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

			By("getting the service account token")
			token, err := serviceAccountToken()
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())

			By("ensuring the controller pod is ready")
			verifyControllerPodReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pod", controllerPodName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Controller pod not ready")
			}
			Eventually(verifyControllerPodReady, 3*time.Minute, time.Second).Should(Succeed())

			By("verifying that the controller manager is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("Serving metrics server"),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted, 3*time.Minute, time.Second).Should(Succeed())

			// +kubebuilder:scaffold:e2e-metrics-webhooks-readiness

			By("creating the curl-metrics pod to access the metrics endpoint")
			cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:latest",
				"--overrides",
				fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["/bin/sh", "-c"],
							"args": [
								"for i in $(seq 1 30); do curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics && exit 0 || sleep 2; done; exit 1"
							],
							"securityContext": {
								"readOnlyRootFilesystem": true,
								"allowPrivilegeEscalation": false,
								"capabilities": {
									"drop": ["ALL"]
								},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {
									"type": "RuntimeDefault"
								}
							}
						}],
						"serviceAccountName": "%s"
					}
				}`, token, metricsServiceName, namespace, serviceAccountName))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

			By("waiting for the curl-metrics pod to complete.")
			verifyCurlUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
			}
			Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

			By("getting the metrics by checking curl-metrics logs")
			verifyMetricsAvailable := func(g Gomega) {
				metricsOutput, err := getMetricsOutput()
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
				g.Expect(metricsOutput).NotTo(BeEmpty())
				g.Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
			}
			Eventually(verifyMetricsAvailable, 2*time.Minute).Should(Succeed())
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks

		// TODO: Customize the e2e test suite with scenarios specific to your project.
		// Consider applying sample/CR(s) and check their status and/or verifying
		// the reconciliation by using the metrics, i.e.:
		// metricsOutput, err := getMetricsOutput()
		// Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
		// Expect(metricsOutput).To(ContainSubstring(
		//    fmt.Sprintf(`controller_runtime_reconcile_total{controller="%s",result="success"} 1`,
		//    strings.ToLower(<Kind>),
		// ))
	})
})

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	By("creating temporary file to store the token request")
	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}

	var out string
	verifyTokenCreation := func(g Gomega) {
		By("executing kubectl command to create the token")
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred())

		By("parsing the JSON output to extract the token")
		var token tokenRequest
		err = json.Unmarshal(output, &token)
		g.Expect(err).NotTo(HaveOccurred())

		out = token.Status.Token
	}
	Eventually(verifyTokenCreation).Should(Succeed())

	return out, err
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() (string, error) {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	return utils.Run(cmd)
}

func applyYAML(manifest string) error {
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	_, err := utils.Run(cmd)
	return err
}

func fixtureNamespaceYAML() string {
	return fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
`, fixtureNamespace)
}

func documentAuthorizationModuleYAML() string {
	return fmt.Sprintf(`apiVersion: authz.erli.ng/v1
kind: AuthorizationModule
metadata:
  name: document-authorization
  namespace: %s
spec:
  resource: document
  roles:
    viewer:
      subjects:
      - type: user
  permissions:
    can_view:
      anyOf:
      - viewer
`, fixtureNamespace)
}

func duplicateDocumentAuthorizationModuleYAML() string {
	return fmt.Sprintf(`apiVersion: authz.erli.ng/v1
kind: AuthorizationModule
metadata:
  name: duplicate-document-authorization
  namespace: %s
spec:
  resource: document
  roles:
    editor:
      subjects:
      - type: user
`, fixtureNamespace)
}

func waitAuthorizationModuleReady(moduleNamespace, name, status, reason string) {
	Eventually(func(g Gomega) {
		condition, err := authorizationModuleReadyCondition(moduleNamespace, name)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(condition.Status).To(Equal(status))
		g.Expect(condition.Reason).To(Equal(reason))
	}, 3*time.Minute, time.Second).Should(Succeed())
}

func authorizationModuleReadyCondition(moduleNamespace, name string) (conditionStatus, error) {
	cmd := exec.Command("kubectl", "-n", moduleNamespace, "get", "authorizationmodule", name, "-o", "json")
	output, err := utils.Run(cmd)
	if err != nil {
		return conditionStatus{}, err
	}

	var module authorizationModule
	if err := json.Unmarshal([]byte(output), &module); err != nil {
		return conditionStatus{}, err
	}
	for _, condition := range module.Status.Conditions {
		if condition.Type == "Ready" {
			return condition, nil
		}
	}
	return conditionStatus{}, fmt.Errorf("Ready condition not found on %s/%s", moduleNamespace, name)
}

func waitOpenFGAClientConfigSecret() openFGAClientConfigSecret {
	var secret openFGAClientConfigSecret
	Eventually(func(g Gomega) {
		var err error
		secret, err = readOpenFGAClientConfigSecret()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(secret.Config.StoreId).NotTo(BeEmpty())
		g.Expect(secret.Config.AuthorizationModelId).NotTo(BeEmpty())
	}, 3*time.Minute, time.Second).Should(Succeed())
	return secret
}

func readOpenFGAClientConfigSecret() (openFGAClientConfigSecret, error) {
	cmd := exec.Command("kubectl", "-n", namespace, "get", "secret", openFGAClientConfigSecretName, "-o", "json")
	output, err := utils.Run(cmd)
	if err != nil {
		return openFGAClientConfigSecret{}, err
	}

	var secret kubernetesSecret
	if err := json.Unmarshal([]byte(output), &secret); err != nil {
		return openFGAClientConfigSecret{}, err
	}
	encodedConfig := secret.Data[openFGAClientConfigKey]
	if encodedConfig == "" {
		return openFGAClientConfigSecret{}, fmt.Errorf("%s key is missing", openFGAClientConfigKey)
	}
	configJSON, err := base64.StdEncoding.DecodeString(encodedConfig)
	if err != nil {
		return openFGAClientConfigSecret{}, err
	}

	var config openfgaclient.ClientConfiguration
	if err := json.Unmarshal(configJSON, &config); err != nil {
		return openFGAClientConfigSecret{}, err
	}

	return openFGAClientConfigSecret{
		Annotations: secret.Metadata.Annotations,
		Config:      config,
	}, nil
}

func startOpenFGAPortForward() string {
	port := freeTCPPort()
	cmd := exec.Command("kubectl", "-n", namespace, "port-forward", fmt.Sprintf("svc/%s", openFGAServiceName), fmt.Sprintf("%d:8080", port))
	projectDir, err := utils.GetProjectDir()
	Expect(err).NotTo(HaveOccurred())
	cmd.Dir = projectDir
	cmd.Env = append(os.Environ(), "GO111MODULE=on")
	cmd.Stdout = GinkgoWriter
	cmd.Stderr = GinkgoWriter
	Expect(cmd.Start()).To(Succeed())

	DeferCleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	address := fmt.Sprintf("127.0.0.1:%d", port)
	Eventually(func() error {
		conn, err := net.DialTimeout("tcp", address, time.Second)
		if err != nil {
			return err
		}
		return conn.Close()
	}, 30*time.Second, 500*time.Millisecond).Should(Succeed())

	return fmt.Sprintf("http://%s", address)
}

func freeTCPPort() int {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	defer func() {
		Expect(listener.Close()).To(Succeed())
	}()

	return listener.Addr().(*net.TCPAddr).Port
}

func typeHasRelations(model openfga.AuthorizationModel, typeName string, relations ...string) bool {
	for _, typeDefinition := range model.GetTypeDefinitions() {
		if typeDefinition.Type != typeName {
			continue
		}
		typeRelations := typeDefinition.GetRelations()
		for _, relation := range relations {
			if _, ok := typeRelations[relation]; !ok {
				return false
			}
		}
		return true
	}
	return false
}

func expectCheckAllowed(ctx context.Context, client *openfgaclient.OpenFgaClient, user, relation, object string, allowed bool) {
	response, err := client.Check(ctx).
		Body(openfgaclient.ClientCheckRequest{
			User:     user,
			Relation: relation,
			Object:   object,
		}).
		Execute()
	Expect(err).NotTo(HaveOccurred())
	Expect(response.GetAllowed()).To(Equal(allowed))
}

type openFGAClientConfigSecret struct {
	Annotations map[string]string
	Config      openfgaclient.ClientConfiguration
}

type kubernetesSecret struct {
	Metadata struct {
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Data map[string]string `json:"data"`
}

type authorizationModule struct {
	Status struct {
		Conditions []conditionStatus `json:"conditions"`
	} `json:"status"`
}

type conditionStatus struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
