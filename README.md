# service-provider-ocm

An [openMCP](https://github.com/openmcp-project) Service Provider that installs and manages
[OCM K8s Toolkit](https://github.com/open-component-model/open-component-model/tree/main/kubernetes/controller) on
workload clusters via Flux HelmReleases.

[![REUSE status](https://api.reuse.software/badge/github.com/open-component-model/service-provider-ocm)](https://api.reuse.software/info/github.com/open-component-model/service-provider-ocm)


## How It Works

When an `OCM` resource is created on the onboarding cluster, the controller:

1. Resolves `spec.version` from the `OCM` resource against `spec.versions` in the `ProviderConfig`, which yields the chart URL, chart version, pull secret, and Helm values for that version
2. Replicates the version's chart pull secret into the tenant namespace and wires it into the `OCIRepository`
3. Creates a Flux `OCIRepository` pointing at the resolved chart URL and chart version
4. Creates a Flux `HelmRelease` that deploys the chart into `ocm-k8s-toolkit-system` on the workload cluster via a kubeconfig reference

## API Reference

### OCM

The domain service API. Created on the onboarding cluster, one per tenant.

```yaml
apiVersion: ocm.services.open-control-plane.io/v1alpha1
kind: OCM
metadata:
  name: mcp-01 # must match your MCP cluster so it will track the right cluster
spec:
  version: v0.12.0
```

| Field          | Type     | Required | Description                                                                                            |
|----------------|----------|----------|--------------------------------------------------------------------------------------------------------|
| `spec.version` | `string` | yes      | The version to install. Must be one of the versions offered by `spec.versions` in the `ProviderConfig` |

_Note_: Any version a tenant may request has to be defined in the `ProviderConfig`. Requesting a
version that is not on offer leaves the resource in phase `Failed`, with the available versions
listed in the `Ready` condition message.

_Note_: The name of the object _**MUST**_ match the name of your MCP cluster offering. This
is to ensure that no multiple installations can exist for the same cluster.

### ProviderConfig

Cluster-scoped operational configuration. Declares which versions of the ocm-k8s-toolkit
tenants may install, and the deployment artifacts each of those versions maps to.

```yaml
apiVersion: ocm.services.open-control-plane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: ocm # This name here is important!
spec:
  pollInterval: 5m
  versions:
    - version: v0.11.0
      chartVersion: "0.11.0"
      chartURL: oci://ghcr.io/open-component-model/kubernetes/controller/chart
    # renovate: datasource=docker depName=ghcr.io/open-component-model/kubernetes/controller/chart
    - version: v0.12.0
      chartVersion: "0.12.0"
      chartURL: oci://ghcr.io/open-component-model/kubernetes/controller/chart
      chartPullSecret: my-registry-secret
      helmValues:
        manager:
          concurrency:
            resource: 10
          imagePullSecrets:
            - name: my-registry-secret
```

#### `spec`

| Field          | Type       | Required | Default | Description                                                                             |
|----------------|------------|----------|---------|-----------------------------------------------------------------------------------------|
| `versions`     | `array`    | yes      | —       | The versions of the ocm-k8s-toolkit that can be installed. Must have at least one entry |
| `pollInterval` | `duration` | no       | `1m`    | How often the controller polls for changes                                              |

A version item is defined as follows:

| Field             | Type     | Required | Default                                                          | Description                                                                                                                                                                           |
|-------------------|----------|----------|------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `version`         | `string` | yes      | —                                                                | The tenant version this item defines. Compared against `OCM.spec.version`                                                                                                             |
| `chartVersion`    | `string` | yes      | —                                                                | The Helm chart tag to install. Need not equal `version`, the tenant version may have a `v` prefix or be a product version that differs from the chart tag                             |
| `chartURL`        | `string` | no       | `oci://ghcr.io/open-component-model/kubernetes/controller/chart` | OCI URL of the Helm chart (`oci://` prefix is added automatically if missing)                                                                                                         |
| `chartPullSecret` | `string` | no       | —                                                                | Name of a secret in the controller's namespace to replicate into the tenant namespace and set as `secretRef` on the `OCIRepository`. Must be of type `kubernetes.io/dockerconfigjson` |
| `helmValues`      | `object` | no       | —                                                                | Arbitrary Helm values passed directly to the HelmRelease. Secrets under `manager.imagePullSecrets` are replicated into the ocm-k8s-toolkit namespace on the control plane             |

Because every artifact is declared per version, two versions can point at different registries,
use different pull secrets, or have different Helm values.

Deleting an `OCM` resource does not require its version to still be on offer. It does block upgrades and re-reconciles
of instances still requesting it, so remove a version only once no instance references it.

## How the OCM K8s toolkit works

Check out the [controller concept](https://ocm.software/docs/concepts/ocm-controllers/) and our guides, e.g.
[Deploy Helm Charts](https://ocm.software/docs/getting-started/deploy-helm-charts/).

## Running E2E Tests

```shell
task test-e2e
```

## Contributing

Please refer to the [CONTRIBUTING.md](CONTRIBUTING.md) for information on how to set up your
development environment, run tests, and submit pull requests.

For the general OCM contribution process, see the
[OCM Contributing Guide](https://ocm.software/community/contributing/).

OCM follows the [Linux Foundation EU Code of Conduct](https://linuxfoundation.eu/policies/code-of-conduct).

## Licensing

Please see our [LICENSE](LICENSE) for copyright and license information.
Detailed information including third-party components and their licensing/copyright information is available
[via the REUSE tool](https://api.reuse.software/info/github.com/open-component-model/service-provider-ocm).

---

<p align="center"><img alt="Bundesministerium für Wirtschaft und Energie (BMWE)-EU funding logo" src="https://apeirora.eu/assets/img/BMWK-EU.png" width="400"/></p>