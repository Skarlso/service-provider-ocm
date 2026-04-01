# service-provider-ocm

An [openMCP](https://github.com/openmcp-project) Service Provider that installs and manages
[OCM K8s Toolkit](https://github.com/open-component-model/open-component-model/tree/main/kubernetes/controller) on
workload clusters via Flux HelmReleases.

[![REUSE status](https://api.reuse.software/badge/github.com/open-component-model/service-provider-ocm)](https://api.reuse.software/info/github.com/open-component-model/service-provider-ocm)


## How It Works

When an `OCM` resource is created on the onboarding cluster, the controller:

1. Replicates the configured image pull secret into the tenant namespace and wires it into the `OCIRepository`
2. Creates a Flux `OCIRepository` pointing at the chart URL from the `ProviderConfig` and the version from the `OCM` spec
3. Creates a Flux `HelmRelease` that deploys the chart into `ocm-k8s-toolkit-system` on the workload cluster via a kubeconfig reference

## API Reference

### OCM

The domain service API. Created on the onboarding cluster, one per tenant.

```yaml
apiVersion: ocm.services.openmcp.cloud/v1alpha1
kind: OCM
metadata:
  name: mcp-01 # must match your MCP cluster so it will track the right cluster
spec:
  # renovate: datasource=docker depName=ghcr.io/open-component-model/kubernetes/controller/chart
  version: 0.3.0
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `spec.version` | `string` | yes | Chart version tag |

_Note_: The name of the object _**MUST**_ match the name of your MCP cluster offering. This
is to ensure that no multiple installations can exist for the same cluster.

### ProviderConfig

Cluster-scoped operational configuration. Controls the chart location, image pull
secret replication, and Helm values passed to managed HelmReleases.

```yaml
apiVersion: ocm.services.openmcp.cloud/v1alpha1
kind: ProviderConfig
metadata:
  name: ocm # This name here is important!
spec:
  pollInterval: 5m
  chartURL: ghcr.io/open-component-model/kubernetes/controller/chart
  imagePullSecret:
    name: my-registry-secret
  values:
    manager:
      concurrency:
        resource: 10
```

#### `spec`

| Field | Type | Required | Default                                                    | Description |
|-------|------|----------|------------------------------------------------------------|-------------|
| `chartURL` | `string` | no | `ghcr.io/open-component-model/kubernetes/controller/chart` | OCI URL of the Helm chart (`oci://` prefix is added automatically if missing) |
| `pollInterval` | `duration` | no | `1m`                                                       | How often the controller polls for changes |
| `imagePullSecret` | `LocalObjectReference` | no | —                                                          | Secret to replicate from the controller's namespace into tenant namespaces and set as `secretRef` on the `OCIRepository` |
| `values` | `object` | no | —                                                          | Arbitrary Helm values passed directly to the HelmRelease |

## How the OCM K8s toolkit works

Check out the [controller concept](https://ocm.software/docs/concepts/ocm-controllers/) and our guides, e.g.
[Deploy Helm Charts](https://ocm.software/docs/getting-started/deploy-helm-charts/).

## Running E2E Tests

```shell
task test-e2e
```

## Contributing

Code contributions, feature requests, bug reports, and help requests are very welcome. Please refer to the
[Contributing Guide in the Community repository](https://github.com/open-component-model/.github/blob/main/CONTRIBUTING.md)
for more information on how to contribute to OCM.

OCM follows the [NeoNephos Code of Conduct](https://github.com/neonephos/.github/blob/main/CODE_OF_CONDUCT.md).

## Licensing

Please see our [LICENSE](LICENSE) for copyright and license information.
Detailed information including third-party components and their licensing/copyright information is available
[via the REUSE tool](https://api.reuse.software/info/github.com/open-component-model/service-provider-ocm).

---

<p align="center"><img alt="Bundesministerium für Wirtschaft und Energie (BMWE)-EU funding logo" src="https://apeirora.eu/assets/img/BMWK-EU.png" width="400"/></p>