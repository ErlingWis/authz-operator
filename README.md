# bridder
// TODO(user): Add simple overview of use/purpose

## Description
// TODO(user): An in-depth paragraph about your project and overview of use

## Getting Started

### Prerequisites
- go version v1.24.6+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### External dependencies

Bridder does not deploy OpenFGA or NATS in the default release install. Create
those services separately, then provide their connection settings in the
`bridder-system/bridder-config` Secret before deploying Bridder:

```sh
kubectl create namespace bridder-system
kubectl -n bridder-system create secret generic bridder-config \
  --from-literal=OPENFGA_API_URL=https://openfga.example.com \
  --from-literal=OPENFGA_STORE_NAME=bridder \
  --from-literal=NATS_URL=nats://nats.example.com:4222
```

Optional keys are `OPENFGA_STORE_ID`, `OPENFGA_API_TOKEN`, `NATS_TOKEN`,
`NATS_STREAM`, `NATS_SUBJECT`, `NATS_CONSUMER`, `NATS_DLQ_SUBJECT`, and
`TUPLE_LISTENER_BATCH_SIZE`.

For local development, deploy the bundled dependency stack instead:

```sh
kubectl apply -k config/dev
```

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/bridder:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/bridder:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the `AuthorizationModule` samples from `config/samples`:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

The controller creates the managed `AuthorizationModelRelease` in the
`bridder-system` namespace. Promote a published candidate by patching that
release with the candidate hash:

```sh
kubectl -n bridder-system patch authorizationmodelrelease bridder-authorization-model \
  --type merge \
  -p '{"spec":{"stableModelHash":"<candidate-model-hash>"}}'
```

The `bridder-authorization-model-proxy` Service exposes the stable authorization
model on port `8080`. It serves the AuthZEN discovery and access endpoints, and
forwards AuthZEN access requests to the configured OpenFGA API using the
promoted stable store and model IDs.

Bridder-specific metadata endpoints are also available:

```sh
GET /bridder/v1/resource-types
GET /bridder/v1/resource-types/<resource-type>/access-schema
GET /bridder/v1/role-assignments?resourceType=<resource-type>&resourceId=<resource-id>
```

The role assignment endpoint reports direct role tuples on the requested
resource. It does not expand inherited or computed permissions.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/bridder:tag
```

**NOTE:** The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without its
dependencies.

2. Using the installer

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install
the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/bridder/<tag or branch>/dist/install.yaml
```

### By providing a Helm Chart

1. Build the chart using the optional helm plugin

```sh
kubebuilder edit --plugins=helm/v2-alpha
```

2. See that a chart was generated under 'dist/chart', and users
can obtain this solution from there.

**NOTE:** If you change the project, you need to update the Helm Chart
using the same command above to sync the latest changes. Furthermore,
if you create webhooks, you need to use the above command with
the '--force' flag and manually ensure that any custom configuration
previously added to 'dist/chart/values.yaml' or 'dist/chart/manager/manager.yaml'
is manually re-applied afterwards.

## Contributing
// TODO(user): Add detailed information on how you would like others to contribute to this project

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

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
