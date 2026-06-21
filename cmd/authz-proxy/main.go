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

package main

import (
	"net/http"
	"os"
	"time"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	authv1 "erli.ng/authz-operator/api/v1"
	"erli.ng/authz-operator/internal/authzproxy"
)

const (
	openFGAAPIURLEnv       = "OPENFGA_API_URL"
	openFGAAPITokenEnv     = "OPENFGA_API_TOKEN"
	authzProxyAddressEnv   = "AUTHZ_PROXY_ADDRESS"
	authzProxyNamespaceEnv = "AUTHZ_PROXY_NAMESPACE"
	authzProxyReleaseEnv   = "AUTHZ_PROXY_RELEASE_NAME"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(authv1.AddToScheme(scheme))
}

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	k8sClient, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: scheme})
	if err != nil {
		setupLog.Error(err, "Failed to create Kubernetes client")
		os.Exit(1)
	}

	server := &authzproxy.Server{
		Client:      k8sClient,
		APIURL:      os.Getenv(openFGAAPIURLEnv),
		APIToken:    os.Getenv(openFGAAPITokenEnv),
		Namespace:   authzproxy.DefaultNamespace,
		ReleaseName: authzproxy.DefaultReleaseName,
	}
	if namespace := os.Getenv(authzProxyNamespaceEnv); namespace != "" {
		server.Namespace = namespace
	}
	if releaseName := os.Getenv(authzProxyReleaseEnv); releaseName != "" {
		server.ReleaseName = releaseName
	}
	address := authzproxy.DefaultBindAddress
	if configured := os.Getenv(authzProxyAddressEnv); configured != "" {
		address = configured
	}

	setupLog.Info("Starting authorization proxy", "address", address)
	httpServer := &http.Server{
		Addr:              address,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := httpServer.ListenAndServe(); err != nil {
		setupLog.Error(err, "Failed to run authorization proxy")
		os.Exit(1)
	}
}
