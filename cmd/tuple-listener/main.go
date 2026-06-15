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
	"os"
	"strconv"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	authv1 "my.domain/fga/api/v1"
	"my.domain/fga/internal/tuplelistener"
	"my.domain/fga/internal/tuplewriter"
)

const (
	openFGAAPIURLEnv          = "OPENFGA_API_URL"
	openFGAAPITokenEnv        = "OPENFGA_API_TOKEN"
	tupleListenerNamespaceEnv = "TUPLE_LISTENER_NAMESPACE"
	tupleListenerReleaseEnv   = "TUPLE_LISTENER_RELEASE_NAME"
	natsURLEnv                = "NATS_URL"
	natsTokenEnv              = "NATS_TOKEN"
	natsStreamEnv             = "NATS_STREAM"
	natsSubjectEnv            = "NATS_SUBJECT"
	natsConsumerEnv           = "NATS_CONSUMER"
	natsDLQSubjectEnv         = "NATS_DLQ_SUBJECT"
	tupleListenerBatchSizeEnv = "TUPLE_LISTENER_BATCH_SIZE"
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

	service := &tuplewriter.Service{
		Resolver: &tuplewriter.StableReleaseResolver{
			Client:      k8sClient,
			Namespace:   envOrDefault(tupleListenerNamespaceEnv, tuplewriter.DefaultNamespace),
			ReleaseName: envOrDefault(tupleListenerReleaseEnv, tuplewriter.DefaultReleaseName),
		},
		Writer: &tuplewriter.OpenFGAWriter{
			APIURL:   os.Getenv(openFGAAPIURLEnv),
			APIToken: os.Getenv(openFGAAPITokenEnv),
		},
	}

	config := tuplelistener.NATSConfig{
		URL:        os.Getenv(natsURLEnv),
		Token:      os.Getenv(natsTokenEnv),
		Stream:     os.Getenv(natsStreamEnv),
		Subject:    os.Getenv(natsSubjectEnv),
		Consumer:   os.Getenv(natsConsumerEnv),
		DLQSubject: os.Getenv(natsDLQSubjectEnv),
		BatchSize:  envIntOrDefault(tupleListenerBatchSizeEnv, tuplelistener.DefaultBatchSize),
	}

	setupLog.Info("Starting tuple listener")
	if err := tuplelistener.RunNATS(ctrl.SetupSignalHandler(), config, service); err != nil {
		setupLog.Error(err, "Failed to run tuple listener")
		os.Exit(1)
	}
}

func envOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func envIntOrDefault(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return defaultValue
	}
	return parsed
}
