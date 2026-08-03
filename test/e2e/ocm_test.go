package e2e

import (
	"context"
	"testing"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	libutils "github.com/openmcp-project/openmcp-operator/lib/utils"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	apiv1alpha1 "github.com/open-component-model/service-provider-ocm/api/v1alpha1"

	"github.com/openmcp-project/openmcp-testing/pkg/clusterutils"
	openmcpconditions "github.com/openmcp-project/openmcp-testing/pkg/conditions"
	"github.com/openmcp-project/openmcp-testing/pkg/providers"
	"github.com/openmcp-project/openmcp-testing/pkg/resources"
)

func TestServiceProvider(t *testing.T) {
	var onboardingList unstructured.UnstructuredList
	mcpName := "test-mcp"
	basicProviderTest := features.New("provider test").
		Setup(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			if _, err := resources.CreateObjectsFromDir(ctx, c, "platform"); err != nil {
				t.Errorf("failed to create platform cluster objects: %v", err)
			}
			return ctx
		}).
		Setup(providers.CreateMCP(mcpName)).
		Assess("verify service can be successfully consumed",
			func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
				onboardingConfig, err := clusterutils.OnboardingConfig()
				if err != nil {
					t.Error(err)
					return ctx
				}
				objList, err := resources.CreateObjectsFromDir(ctx, onboardingConfig, "onboarding")
				if err != nil {
					t.Errorf("failed to create onboarding cluster objects: %v", err)
					return ctx
				}
				for _, obj := range objList.Items {
					if err := wait.For(openmcpconditions.Match(&obj, onboardingConfig, "Ready", corev1.ConditionTrue)); err != nil {
						t.Error(err)
					}
				}
				objList.DeepCopyInto(&onboardingList)
				return ctx
			},
		). // Add Assess to verify that helmRelese and Oci Repo are Ready.
		Assess("verify that helm release and oci repository are ready", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			tenantNamespace, err := libutils.StableMCPNamespace(mcpName, "default")
			if err != nil {
				t.Errorf("failed to get tenant namespace: %v", err)
				return ctx
			}

			helmRelease := &helmv2.HelmRelease{}
			helmRelease.SetName("sp-ocm-k8s-toolkit")
			helmRelease.SetNamespace(tenantNamespace)

			ociRepo := &sourcev1.OCIRepository{}
			ociRepo.SetName("sp-ocm-k8s-toolkit")
			ociRepo.SetNamespace(tenantNamespace)

			chartSecret := &corev1.Secret{}
			chartSecret.SetName("sp-ocm-privateregcred")
			chartSecret.SetNamespace(tenantNamespace)
			pullSecrets := &corev1.SecretList{
				Items: []corev1.Secret{*chartSecret},
			}

			if err := wait.For(openmcpconditions.Match(helmRelease, config, "Ready", corev1.ConditionTrue), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("HelmRelease not ready: %v", err)
			}
			if err := wait.For(openmcpconditions.Match(ociRepo, config, "Ready", corev1.ConditionTrue), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("OCIRepository not ready: %v", err)
			}
			if err := wait.For(conditions.New(config.Client().Resources()).ResourcesFound(pullSecrets), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("pull secret not found: %v", err)
			}

			mcpConfig, err := clusterutils.MCPConfig(ctx, config, mcpName)
			if err != nil {
				t.Errorf("failed to get mcp config: %v", err)
				return ctx
			}
			mcpPullSecret := &corev1.Secret{}
			mcpPullSecret.SetName("privateregcred")
			mcpPullSecret.SetNamespace("ocm-k8s-toolkit-system")
			mcpPullSecrets := &corev1.SecretList{Items: []corev1.Secret{*mcpPullSecret}}
			if err := wait.For(conditions.New(mcpConfig.Client().Resources()).ResourcesFound(mcpPullSecrets), wait.WithTimeout(2*time.Minute)); err != nil {
				t.Errorf("mcp pull secret not found: %v", err)
			}

			return ctx
		}).
		Assess("resources for the ocm object contain helm release and oci repository", func(ctx context.Context, t *testing.T, config *envconf.Config) context.Context {
			onboardingConfig, err := clusterutils.OnboardingConfig()
			if err != nil {
				t.Error(err)
				return ctx
			}

			scheme := onboardingConfig.Client().Resources().GetScheme()
			if err := apiv1alpha1.AddToScheme(scheme); err != nil {
				t.Errorf("failed to register ocm scheme: %v", err)
				return ctx
			}

			ocm := &apiv1alpha1.OCM{}
			if err := onboardingConfig.Client().Resources().Get(ctx, mcpName, corev1.NamespaceDefault, ocm); err != nil {
				t.Errorf("failed to get OCM object: %v", err)
				return ctx
			}

			var hasOCI, hasHelm bool
			for _, r := range ocm.Status.Resources {
				switch r.Kind {
				case "OCIRepository":
					hasOCI = true
					if r.Phase != apiv1alpha1.Ready {
						t.Errorf("OCIRepository phase is %q, want %q", r.Phase, apiv1alpha1.Ready)
					}
				case "HelmRelease":
					hasHelm = true
					if r.Phase != apiv1alpha1.Ready {
						t.Errorf("HelmRelease phase is %q, want %q", r.Phase, apiv1alpha1.Ready)
					}
				}
			}
			if !hasOCI {
				t.Errorf("OCM status resources do not contain OCIRepository, got: %v", ocm.Status.Resources)
			}
			if !hasHelm {
				t.Errorf("OCM status resources do not contain HelmRelease, got: %v", ocm.Status.Resources)
			}

			return ctx
		}).
		Assess("verify domain objects can be created", providers.ImportDomainAPIs(mcpName, "mcp")).
		Teardown(func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
			// Remove the ocm Repository from the MCP first, otherwise the OCM
			// deletion is blocked to avoid orphaning its managed resources.
			mcpConfig, err := clusterutils.MCPConfig(ctx, c, mcpName)
			if err != nil {
				t.Errorf("failed to get MCP config: %v", err)
			}

			repo := &unstructured.Unstructured{}
			repo.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "delivery.ocm.software",
				Version: "v1alpha1",
				Kind:    "Repository",
			})
			repo.SetName("platform-mesh")
			repo.SetNamespace("default")
			if err := resources.DeleteObject(ctx, mcpConfig, repo, wait.WithTimeout(time.Minute)); err != nil {
				t.Errorf("failed to delete Repository from MCP: %v", err)
			}

			onboardingConfig, err := clusterutils.OnboardingConfig()
			if err != nil {
				t.Error(err)
				return ctx
			}
			for _, obj := range onboardingList.Items {
				if err := resources.DeleteObject(ctx, onboardingConfig, &obj, wait.WithTimeout(time.Minute)); err != nil {
					t.Errorf("failed to delete onboarding object: %v", err)
				}
			}
			return ctx
		}).
		Teardown(providers.DeleteMCP(mcpName, wait.WithTimeout(5*time.Minute)))
	testenv.Test(t, basicProviderTest.Feature())
}
