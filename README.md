# service-provider-ocm

An [openMCP](https://github.com/openmcp-project) Service Provider that installs and manages
[OCM K8s Toolkit](https://github.com/open-component-model/ocm-k8s-toolkit) on workload clusters
via Flux HelmReleases.

## How It Works

When an `OCM` resource is created on the onboarding cluster, the controller:

1. Replicates the configured image pull secret into the tenant namespace and wires it into the `OCIRepository`
2. Creates a Flux `OCIRepository` pointing at the chart URL from the `ProviderConfig` and the version from the `OCM` spec
3. Creates a Flux `HelmRelease` that deploys the chart into `ocm-system` on the workload cluster via a kubeconfig reference

## API Reference

### OCM

The domain service API. Created on the onboarding cluster, one per tenant.

```yaml
apiVersion: ocm.services.openmcp.cloud/v1alpha1
kind: OCM
metadata:
  name: my-ocm
spec:
  version: v0.1.0
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `spec.version` | `string` | yes | Chart version tag |

### ProviderConfig

Cluster-scoped operational configuration. Controls the chart location, image pull
secret replication, and Helm values passed to managed HelmReleases.

```yaml
apiVersion: ocm.services.openmcp.cloud/v1alpha1
kind: ProviderConfig
metadata:
  name: ocm-provider-config
spec:
  pollInterval: 5m
  chartURL: ghcr.io/open-component-model/charts/ocm-k8s-toolkit
  imagePullSecret:
    name: my-registry-secret
  values:
    manager:
      concurrency:
        resource: 10
```

#### `spec`

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `chartURL` | `string` | no | `ghcr.io/open-component-model/charts/ocm-k8s-toolkit` | OCI URL of the Helm chart (`oci://` prefix is added automatically if missing) |
| `pollInterval` | `duration` | no | `1m` | How often the controller polls for changes |
| `imagePullSecret` | `LocalObjectReference` | no | — | Secret to replicate from the controller's namespace into tenant namespaces and set as `secretRef` on the `OCIRepository` |
| `values` | `object` | no | — | Arbitrary Helm values passed directly to the HelmRelease |

## Running E2E Tests

```shell
task test-e2e
```

## Licensing

Copyright 2025 SAP SE or an SAP affiliate company and service-provider-ocm contributors.
See [LICENSE](LICENSE) for details.
