# Contributing to service-provider-ocm

For the general contribution process (fork-and-pull workflow, commit requirements, DCO,
code of conduct, and more), see the
[OCM Contributing Guide](https://ocm.software/community/contributing/).

This document covers repository-specific setup and development workflows.

## Prerequisites

- **Go 1.26+**
- **[Task](https://taskfile.dev/) v3.x** - runs all build, test, and lint commands
- **Docker** - required for image builds and E2E tests
- **[Flux CLI](https://fluxcd.io/flux/installation/)** - required for E2E tests
- **kubectl** - cluster interaction
- **[Kind](https://kind.sigs.k8s.io/)** - local cluster provisioning for E2E tests

## Getting Started

```bash
# Clone with submodules (hack/common is the shared build toolchain)
git clone --recurse-submodules https://github.com/open-component-model/service-provider-ocm.git
cd service-provider-ocm

# If already cloned without submodules
git submodule update --init --recursive
```

## Project Structure

```text
.
├── api/v1alpha1/          # CRD type definitions (OCM, ProviderConfig)
├── cmd/                   # Controller entrypoint
├── internal/controller/   # Reconciler implementations
├── pkg/runtime/           # Shared service-provider runtime utilities
├── test/e2e/              # End-to-end tests
├── hack/common/           # Shared build toolchain (git submodule -> openmcp-project/build)
├── Taskfile.yaml          # Project-specific task definitions
├── VERSION                # Release version (semver or semver-dev)
└── .github/workflows/     # CI/CD pipelines
```

## Common Tasks

```bash
# Generate code (deepcopy, CRDs, formatting)
task generate

# Run linters and validation
task validate

# Run unit tests
task test

# Build container image for local platform
task build:img:build

# Build image and run E2E tests
task test-e2e
```

## Development Workflow

For the general fork-and-pull workflow, commit signing requirements, and DCO sign-off, refer to
the [OCM Contributing Guide](https://ocm.software/community/contributing/). The steps below cover
the repo-specific workflow after you have your local branch ready.

1. Run `task generate` after modifying API types in `api/v1alpha1/`
2. Run `task validate` to ensure lint and vet pass
3. Run `task test` for unit tests
4. Run `task test-e2e` for full end-to-end validation (see below)
5. Submit a pull request - CI runs the same checks automatically

### Pull Request Requirements

PR descriptions **must** include the following sections (enforced by CI via
[validate-pr-content](https://github.com/openmcp-project/build/blob/main/.github/workflows/validate-pr-content.lib.yaml)):

~~~markdown
**What this PR does / why we need it**:

<your description>

**Release note**:
```other operator
<release note or NONE>
```
~~~

For additional requirements (conventional commits, DCO, commit signing, squash merging), see
the [OCM Contributing Guide](https://ocm.software/community/contributing/).

### E2E Tests

The E2E tests use [sigs.k8s.io/e2e-framework](https://github.com/kubernetes-sigs/e2e-framework)
and:

1. Build a container image for the controller
2. Bootstrap a local kind cluster with Flux and the openMCP operator
3. Deploy the service provider and verify reconciliation

## Local Development with openMCP

To test the service provider in a full openMCP environment, use the
[cluster-provider-kind](https://github.com/openmcp-project/cluster-provider-kind) local
development setup. See the
[Development section](https://github.com/openmcp-project/cluster-provider-kind#quick-setup-with-local-development-script)
of its README for full details.

### 1. Bootstrap the Platform

```bash
# Clone the cluster-provider-kind repository
git clone https://github.com/openmcp-project/cluster-provider-kind.git
cd cluster-provider-kind

# Deploy a full openMCP local environment (KinD cluster + operator + service providers + Flux)
# Skip other service providers for faster iteration if you only need OCM:
DEPLOY_SP_CROSSPLANE=false DEPLOY_SP_LANDSCAPER=false ./hack/local-dev.sh deploy
```

### 2. Access the Platform Cluster

```bash
# Get a kubeconfig for the platform cluster (written to a temporary file)
./hack/local-dev.sh access-platform-cluster
```

To access other clusters (e.g. `onboarding`, `mcp` clusters):

```bash
kind get clusters
kind get kubeconfig --name <cluster-name> > <cluster-name>.kubeconfig
```

### 3. Build and Load the Service Provider Image

```bash
# Back in the service-provider-ocm directory
cd /path/to/service-provider-ocm

# Build the container image for your local platform
task build:img:build
```

Make sure to note the image tag printed by the build task (e.g.
`ghcr.io/open-component-model/images/service-provider-ocm:v0.1.2-dev-abc123-platform-arch`).
Then load that image into the `platform` cluster:

```bash
# Load the image into the platform cluster (or push to a registry accessible by the cluster)
kind load docker-image --name platform ghcr.io/open-component-model/images/service-provider-ocm:<version>
```

### 4. Install the Service Provider

Install the service provider on the `platform` cluster by creating a `ServiceProvider` custom resource that points to
the image you just built and loaded.

```bash
cat <<EOF > service-provider.yaml
apiVersion: open-control-plane.io/v1alpha1
kind: ServiceProvider
metadata:
  name: ocm
spec:
  image: ghcr.io/open-component-model/images/service-provider-ocm:<version>
EOF
```

Then, apply the manifest on the `platform` cluster:

```bash
kubectl create -f service-provider.yaml
```

### 5. Deploy the ProviderConfig and Create an Instance

When the service provider is ready, you can create the OCM `ProviderConfig`.

```bash
cat <<EOF > provider-config.yaml
apiVersion: ocm.services.open-control-plane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: ocm
spec:
  pollInterval: 1m
  versions:
    - version: v0.12.0
      chartVersion: "0.12.0"
      chartURL: oci://ghcr.io/open-component-model/kubernetes/controller/chart
EOF
```

Create it on the `platform` cluster:

```bash
kubectl create -f provider-config.yaml
```

When the `ProviderConfig` is ready, you can create an `OCM` instance.

```bash
cat <<EOF > ocm-instance.yaml
apiVersion: ocm.services.open-control-plane.io/v1alpha1
kind: OCM
metadata:
  name: test # must match your MCP cluster name; KUBECONFIG=<onboarding cluster kubeconfig> kubectl get mcpv2
spec:
  version: v0.12.0 # must be one of the versions offered by the ProviderConfig above
EOF
```

Create it on the `onboarding` cluster:

```bash
kubectl create -f ocm-instance.yaml
```

### 6. Verify the MCP has the OCM K8s toolkit deployed

To verify that the service provider is working end-to-end, check that the `ocm-k8s-toolkit-system` namespace
is created and the OCM K8s toolkit is running. Run on the `mcp` cluster:

```bash
kubectl get pods -n ocm-k8s-toolkit-system
```

### 7. Reset the Environment

```bash
cd /path/to/cluster-provider-kind
./hack/local-dev.sh reset
```

For the full service provider development lifecycle, see the
[Service Provider Development Guide](https://openmcp-project.github.io/docs/developers/serviceprovider/service-providers).

## Further Reading

- [Service Provider Development Guide](https://openmcp-project.github.io/docs/developers/serviceprovider/service-providers) -
  design, development, testing, and deployment of openMCP service providers
- [General Controller Guidelines](https://openmcp-project.github.io/docs/developers/general) -
  operation annotations, status reporting, event filtering
- [openMCP Documentation](https://openmcp-project.github.io/docs/) -
  full platform documentation
- [openmcp-project/build](https://github.com/openmcp-project/build) -
  shared build toolchain (the `hack/common` submodule)
- [cluster-provider-kind](https://github.com/openmcp-project/cluster-provider-kind) -
  local development environment for the openMCP platform
