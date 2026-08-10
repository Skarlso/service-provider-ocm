/*
Copyright 2025.

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
	"fmt"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	ctrlutils "github.com/openmcp-project/controller-utils/pkg/controller"
	ctrlerrors "github.com/openmcp-project/controller-utils/pkg/errors"
	clustersv1alpha1 "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"
	libutils "github.com/openmcp-project/openmcp-operator/lib/utils"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	"github.com/openmcp-project/openmcp-operator/lib/clusteraccess"

	apiv1alpha1 "github.com/open-component-model/service-provider-ocm/api/v1alpha1"
	spruntime "github.com/open-component-model/service-provider-ocm/pkg/runtime"
)

const (
	// HelmReleaseName is the name of the helmRelease object created for the controller.
	HelmReleaseName = "sp-ocm-k8s-toolkit"
	// OCIRepositoryName is the name of the oci repository object pointing to the helm chart of the controller.
	OCIRepositoryName = "sp-ocm-k8s-toolkit"
	// OcmSystemNamespace is the default namespace on the target cluster to use to install the ocm-k8s-toolkit controller into.
	OcmSystemNamespace = "ocm-k8s-toolkit-system"
	// requestSuffixMCP is the suffix used for the mcp cluster.
	requestSuffixMCP = "--mcp"
	// secretNamePrefix is used to prefix the chart pull secret copy in the tenant namespace on the platform cluster.
	secretNamePrefix = "sp-ocm-"

	// ocmAPIGroup is the API group under which the ocm-k8s-toolkit serves its custom resources.
	ocmAPIGroup = "delivery.ocm.software"
	// ocmAPIVersion is the API version of the ocm-k8s-toolkit's Repository resource.
	ocmAPIVersion = "v1alpha1"
	// repositoryListKind is the list kind of the ocm-k8s-toolkit Repository resource.
	repositoryListKind = "RepositoryList"
	// deletionBlockedRequeue is how long to wait before re-checking whether the
	// user's ocm resources have been removed and deletion may proceed.
	deletionBlockedRequeue = 10 * time.Second

	// conditionReasonError is the Ready condition reason used when a reconcile step fails.
	conditionReasonError = "ReconcileError"
)

// clusterAccessName is the name of the access object containing the kubeconfig for the mcp target cluster.
var clusterAccessName = apiv1alpha1.GroupVersion.Group

// OCMReconciler reconciles a OCM object
type OCMReconciler struct {
	// OnboardingCluster is the cluster where this controller watches OCM resources and reacts to their changes.
	OnboardingCluster *clusters.Cluster
	// PlatformCluster is the cluster where this controller is deployed and configured.
	PlatformCluster *clusters.Cluster
	// PodNamespace is the namespace where this controller is deployed in.
	PodNamespace string
}

// CreateOrUpdate is called on every add or update event
func (r *OCMReconciler) CreateOrUpdate(ctx context.Context, svcobj *apiv1alpha1.OCM, providerConfig *apiv1alpha1.ProviderConfig, clusterCtx spruntime.ClusterContext) (ctrl.Result, error) {
	l := logf.FromContext(ctx)
	l.Info("Reconciling OCM resource", "name", svcobj.Name)
	spruntime.StatusProgressing(svcobj, "Reconciling", "Reconcile in progress")
	tenantNamespace, err := libutils.StableMCPNamespace(svcobj.Name, svcobj.Namespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to determine stable namespace for OCM instance: %w", err)
	}

	l.Info("checking tenantNamespace", "namespace", tenantNamespace)

	// Map the tenant requested version onto the artifacts the ProviderConfig offers for it.
	version, err := providerConfig.ResolveVersion(svcobj.Spec.Version)
	if err != nil {
		l.Info("requested version is not offered by the provider config", "version", svcobj.Spec.Version, "error", err.Error())
		spruntime.StatusProgressing(svcobj, conditionReasonError, err.Error())
		return ctrl.Result{}, ctrlerrors.IgnoreInvalidUserInput(err)
	}
	l.Info("resolved requested version", "version", version.Version, "chartVersion", version.ChartVersion, "chartURL", version.GetChartURL())

	prefixedSecretName, err := prefixSecretName(version.ChartPullSecret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error prefixing chart pull secret %q: %w", version.ChartPullSecret, err)
	}

	if err := r.replicateChartPullSecret(ctx, version.ChartPullSecret, types.NamespacedName{Name: prefixedSecretName, Namespace: tenantNamespace}); err != nil {
		spruntime.StatusProgressing(svcobj, conditionReasonError, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to replicate chart pull secret: %w", err)
	}

	ociRepo, err := r.createOrUpdateOCIRepository(ctx, version.GetChartURL(), version.ChartVersion, prefixedSecretName, tenantNamespace)
	if err != nil {
		spruntime.StatusProgressing(svcobj, conditionReasonError, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to reconcile OCI Repository: %w", err)
	}
	if err := r.replicateMCPImagePullSecrets(ctx, clusterCtx.MCPCluster, version.HelmValues); err != nil {
		spruntime.StatusProgressing(svcobj, conditionReasonError, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to replicate MCP image pull secrets: %w", err)
	}
	helmRel, err := r.createOrUpdateHelmRelease(ctx, tenantNamespace, svcobj, version.HelmValues)
	if err != nil {
		spruntime.StatusProgressing(svcobj, conditionReasonError, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to reconcile HelmRelease: %w", err)
	}

	l.Info("Done reconciling OCM resource", "name", svcobj.Name)

	ociPhase, ociMsg := resourceStatus(ociRepo.Status.Conditions)
	hrPhase, hrMsg := resourceStatus(helmRel.Status.Conditions)
	svcobj.Status.Resources = []apiv1alpha1.ManagedResource{
		{
			TypedObjectReference: corev1.TypedObjectReference{
				APIGroup:  new(sourcev1.GroupVersion.Group),
				Kind:      "OCIRepository",
				Name:      OCIRepositoryName,
				Namespace: new(tenantNamespace),
			},
			Phase:    ociPhase,
			Message:  ociMsg,
			Location: apiv1alpha1.PlatformCluster,
		},
		{
			TypedObjectReference: corev1.TypedObjectReference{
				APIGroup:  new(helmv2.GroupVersion.Group),
				Kind:      "HelmRelease",
				Name:      HelmReleaseName,
				Namespace: new(tenantNamespace),
			},
			Phase:    hrPhase,
			Message:  hrMsg,
			Location: apiv1alpha1.PlatformCluster,
		},
	}

	if ociPhase == apiv1alpha1.Ready && hrPhase == apiv1alpha1.Ready {
		spruntime.StatusReady(svcobj)
	} else {
		spruntime.StatusProgressing(svcobj, "Reconciling", "Waiting for managed resources to become ready")
	}
	// The SPReconciler wrapper applies PollInterval as a fallback RequeueAfter.
	return ctrl.Result{}, nil
}

// Delete is called on every delete event.
func (r *OCMReconciler) Delete(ctx context.Context, obj *apiv1alpha1.OCM, _ *apiv1alpha1.ProviderConfig, clusterCtx spruntime.ClusterContext) (ctrl.Result, error) {
	spruntime.StatusTerminating(obj)

	tenantNamespace, err := libutils.StableMCPNamespace(obj.Name, obj.Namespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to determine stable namespace for OCM instance: %w", err)
	}

	// Block deletion while the user still has ocm-k8s-toolkit resources on the
	// control plane, so their managed resources are not orphaned.
	res, blocked, err := r.repositoriesBlockDeletion(ctx, obj, clusterCtx, tenantNamespace)
	if err != nil {
		spruntime.StatusTerminatingWithReason(obj, conditionReasonError, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to check for remaining ocm resources: %w", err)
	}
	if blocked {
		return res, nil
	}

	obj.Status.Resources = managedResources(tenantNamespace, apiv1alpha1.Terminating)

	objects := []client.Object{
		&sourcev1.OCIRepository{
			ObjectMeta: metav1.ObjectMeta{Name: OCIRepositoryName, Namespace: tenantNamespace},
		},
		&helmv2.HelmRelease{
			ObjectMeta: metav1.ObjectMeta{Name: HelmReleaseName, Namespace: tenantNamespace},
		},
	}

	objectsStillExist := false
	for _, managedObj := range objects {
		if err := r.PlatformCluster.Client().Delete(ctx, managedObj); client.IgnoreNotFound(err) != nil {
			spruntime.StatusTerminatingWithReason(obj, conditionReasonError, err.Error())
			return ctrl.Result{}, fmt.Errorf("delete object failed: %w", err)
		}
		if err := r.PlatformCluster.Client().Get(ctx, client.ObjectKeyFromObject(managedObj), managedObj); !apierrors.IsNotFound(err) {
			objectsStillExist = true
		}
	}

	if objectsStillExist {
		return ctrl.Result{
			RequeueAfter: time.Second * 10,
		}, nil
	}

	obj.Status.Resources = nil
	spruntime.StatusReady(obj)
	return ctrl.Result{}, nil
}

// repositoriesBlockDeletion reports whether the control plane still has ocm
// Repository resources, in which case deletion must wait so their managed
// resources are not orphaned. It returns a requeue result and true when blocked.
// Note: We only check repositories because without a repository, usually, nothing works,
// and repositories have deletion prevention in case there is anything still referencing them.
// Therefor it's enough to check for Repository existence because if they don't, usually,
// nothing else does.
func (r *OCMReconciler) repositoriesBlockDeletion(ctx context.Context, obj *apiv1alpha1.OCM, clusterCtx spruntime.ClusterContext, tenantNamespace string) (ctrl.Result, bool, error) {
	if clusterCtx.MCPCluster == nil {
		return ctrl.Result{}, false, nil
	}
	remaining, err := r.countRepositories(ctx, clusterCtx.MCPCluster)
	if err != nil {
		return ctrl.Result{}, false, err
	}
	if remaining == 0 {
		return ctrl.Result{}, false, nil
	}
	msg := fmt.Sprintf("deletion blocked: waiting for %d ocm Repository resource(s) to be removed from the control plane", remaining)
	logf.FromContext(ctx).Info(msg)
	spruntime.StatusTerminatingMessage(obj, msg)
	obj.Status.Resources = managedResources(tenantNamespace, apiv1alpha1.Terminating)
	return ctrl.Result{RequeueAfter: deletionBlockedRequeue}, true, nil
}

// countRepositories returns the number of ocm-k8s-toolkit Repository objects present
// on the given (control plane) cluster. If the kind is not installed this returns 0.
func (r *OCMReconciler) countRepositories(ctx context.Context, mcpCluster *clusters.Cluster) (int, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   ocmAPIGroup,
		Version: ocmAPIVersion,
		Kind:    repositoryListKind,
	})
	if err := mcpCluster.Client().List(ctx, list); err != nil {
		if apimeta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
			return 0, nil
		}
		return 0, err
	}
	return len(list.Items), nil
}

// managedResources returns the set of platform-cluster objects this controller
// owns for an OCM instance, tagged with the given lifecycle phase.
func managedResources(tenantNamespace string, phase apiv1alpha1.InstancePhase) []apiv1alpha1.ManagedResource {
	return []apiv1alpha1.ManagedResource{
		{
			TypedObjectReference: corev1.TypedObjectReference{
				APIGroup:  new(sourcev1.GroupVersion.Group),
				Kind:      "OCIRepository",
				Name:      OCIRepositoryName,
				Namespace: new(tenantNamespace),
			},
			Phase:    phase,
			Location: apiv1alpha1.PlatformCluster,
		},
		{
			TypedObjectReference: corev1.TypedObjectReference{
				APIGroup:  new(helmv2.GroupVersion.Group),
				Kind:      "HelmRelease",
				Name:      HelmReleaseName,
				Namespace: new(tenantNamespace),
			},
			Phase:    phase,
			Location: apiv1alpha1.PlatformCluster,
		},
	}
}

// resourceStatus maps a Flux resource's Ready condition to an InstancePhase.
// Returns Ready with an empty message when ready, otherwise Progressing with
// the Ready condition's message (or empty if the condition is absent).
func resourceStatus(conditions []metav1.Condition) (apiv1alpha1.InstancePhase, string) {
	if apimeta.IsStatusConditionTrue(conditions, meta.ReadyCondition) {
		return apiv1alpha1.Ready, ""
	}
	if cond := apimeta.FindStatusCondition(conditions, meta.ReadyCondition); cond != nil {
		return apiv1alpha1.Progressing, cond.Message
	}
	return apiv1alpha1.Progressing, ""
}

func (r *OCMReconciler) getMcpFluxConfig(ctx context.Context, namespace, objectName string) (*meta.SecretKeyReference, error) {
	mcpAccessRequest := &clustersv1alpha1.AccessRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusteraccess.StableRequestNameFromLocalName(clusterAccessName, objectName) + requestSuffixMCP,
			Namespace: namespace,
		},
	}

	if err := r.PlatformCluster.Client().Get(ctx, client.ObjectKeyFromObject(mcpAccessRequest), mcpAccessRequest); err != nil {
		return nil, fmt.Errorf("failed to get MCP AccessRequest: %w", err)
	}

	return &meta.SecretKeyReference{
		Name: mcpAccessRequest.Status.SecretRef.Name,
		Key:  "kubeconfig",
	}, nil
}

// replicateChartPullSecret copies the named secret from the controller's namespace into the
// tenant namespace on the platform cluster, where the OCIRepository references it.
func (r *OCMReconciler) replicateChartPullSecret(ctx context.Context, secretName string, target types.NamespacedName) error {
	if secretName == "" {
		return nil
	}
	platformClient := r.PlatformCluster.Client()

	sourceSecret := &corev1.Secret{}
	sourceKey := client.ObjectKey{Name: secretName, Namespace: r.PodNamespace}
	if err := platformClient.Get(ctx, sourceKey, sourceSecret); err != nil {
		return fmt.Errorf("failed to get chart pull secret %q from namespace %q: %w", secretName, r.PodNamespace, err)
	}

	targetSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      target.Name,
			Namespace: target.Namespace,
		},
	}
	if _, err := ctrl.CreateOrUpdate(ctx, platformClient, targetSecret, func() error {
		targetSecret.Data = sourceSecret.Data
		targetSecret.Type = sourceSecret.Type
		return nil
	}); err != nil {
		return fmt.Errorf("failed to replicate chart pull secret %q to namespace %q: %w", secretName, target.Namespace, err)
	}

	return nil
}

// replicateMCPImagePullSecrets copies every secret referenced under
// `manager.imagePullSecrets` in the version's Helm values from the controller's
// own namespace on the platform cluster into the ocm-k8s-toolkit-system namespace
// on the MCP cluster, so the deployed controller can pull its images from private
// registries. The target namespace is created if it does not exist.
//
// Cleanup is not required: when the MCP is torn down or the chart namespace is
// removed, the copied secrets are garbage-collected with it.
func (r *OCMReconciler) replicateMCPImagePullSecrets(ctx context.Context, mcpCluster *clusters.Cluster, values *apiextensionsv1.JSON) error {
	helmValues, err := ExtractHelmValues(values)
	if err != nil {
		return err
	}
	if len(helmValues.Manager.ImagePullSecrets) == 0 {
		return nil
	}
	if mcpCluster == nil {
		return fmt.Errorf("mcp cluster is required to replicate image pull secrets but was nil")
	}

	platformClient := r.PlatformCluster.Client()
	mcpClient := mcpCluster.Client()

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: OcmSystemNamespace}}
	if _, err := ctrl.CreateOrUpdate(ctx, mcpClient, ns, func() error { return nil }); err != nil {
		return fmt.Errorf("failed to ensure namespace %q on mcp cluster: %w", OcmSystemNamespace, err)
	}

	for _, ref := range helmValues.Manager.ImagePullSecrets {
		source := &corev1.Secret{}
		sourceKey := client.ObjectKey{Name: ref.Name, Namespace: r.PodNamespace}
		if err := platformClient.Get(ctx, sourceKey, source); err != nil {
			return fmt.Errorf("failed to get image pull secret %q from namespace %q: %w", ref.Name, r.PodNamespace, err)
		}
		target := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ref.Name,
				Namespace: OcmSystemNamespace,
			},
		}
		if _, err := ctrl.CreateOrUpdate(ctx, mcpClient, target, func() error {
			target.Data = source.Data
			target.Type = source.Type
			return nil
		}); err != nil {
			return fmt.Errorf("failed to replicate image pull secret %q to mcp namespace %q: %w", ref.Name, OcmSystemNamespace, err)
		}
	}
	return nil
}

func (r *OCMReconciler) createOrUpdateOCIRepository(ctx context.Context, chartURL, chartVersion, secretName, namespace string) (*sourcev1.OCIRepository, error) {
	ociRepository := createOciRepository(chartURL, secretName, chartVersion, namespace)
	managedObj := &sourcev1.OCIRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ociRepository.Name,
			Namespace: namespace,
		},
	}
	l := logf.FromContext(ctx)
	l.Info("creating OCI Repository", "object", ociRepository)
	if _, err := ctrl.CreateOrUpdate(ctx, r.PlatformCluster.Client(), managedObj, func() error {
		managedObj.Spec = ociRepository.Spec
		return nil
	}); err != nil {
		return nil, err
	}

	return managedObj, nil
}

func (r *OCMReconciler) createOrUpdateHelmRelease(ctx context.Context, namespace string, svcobj *apiv1alpha1.OCM, values *apiextensionsv1.JSON) (*helmv2.HelmRelease, error) {
	helmRelease, err := r.createHelmRelease(ctx, namespace, svcobj, values)
	if err != nil {
		return nil, fmt.Errorf("failed to create helm release: %w", err)
	}
	managedObj := &helmv2.HelmRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      helmRelease.Name,
			Namespace: namespace,
		},
	}
	l := logf.FromContext(ctx)
	l.Info("creating Helm Release", "object", managedObj)
	if _, err := ctrl.CreateOrUpdate(ctx, r.PlatformCluster.Client(), managedObj, func() error {
		managedObj.Spec = helmRelease.Spec
		return nil
	}); err != nil {
		return nil, err
	}

	return managedObj, nil
}

func createOciRepository(chartURL, secretName, chartVersion, namespace string) *sourcev1.OCIRepository {
	var secretRef *meta.LocalObjectReference
	if secretName != "" {
		secretRef = &meta.LocalObjectReference{Name: secretName}
	}

	return &sourcev1.OCIRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      OCIRepositoryName,
			Namespace: namespace,
		},
		Spec: sourcev1.OCIRepositorySpec{
			Interval:  metav1.Duration{Duration: time.Minute},
			URL:       chartURL,
			SecretRef: secretRef,
			Reference: &sourcev1.OCIRepositoryRef{
				Tag: chartVersion,
			},
		},
	}
}

func (r *OCMReconciler) createHelmRelease(ctx context.Context, namespace string, svcobj *apiv1alpha1.OCM, helmValues *apiextensionsv1.JSON) (*helmv2.HelmRelease, error) {
	fluxConfigRef, err := r.getMcpFluxConfig(ctx, namespace, svcobj.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get FluxConfig: %w", err)
	}

	return &helmv2.HelmRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      HelmReleaseName,
			Namespace: namespace,
		},
		Spec: helmv2.HelmReleaseSpec{
			ReleaseName:      apiv1alpha1.DefaultReleaseName,
			Interval:         metav1.Duration{Duration: time.Minute},
			TargetNamespace:  OcmSystemNamespace,
			StorageNamespace: OcmSystemNamespace,
			Install: &helmv2.Install{
				CRDs:            helmv2.Create,
				CreateNamespace: true,
				Remediation: &helmv2.InstallRemediation{
					Retries: 3,
				},
			},
			Upgrade: &helmv2.Upgrade{
				CRDs:          helmv2.CreateReplace,
				CleanupOnFail: true,
				Remediation: &helmv2.UpgradeRemediation{
					Retries:  3,
					Strategy: new(helmv2.RollbackRemediationStrategy),
				},
			},
			ChartRef: &helmv2.CrossNamespaceSourceReference{
				Kind:      "OCIRepository",
				Name:      OCIRepositoryName,
				Namespace: namespace,
			},
			Values: helmValues,
			KubeConfig: &meta.KubeConfigReference{
				SecretRef: fluxConfigRef,
			},
		},
	}, nil
}

// prefixSecretName adds the "sp-ocm-" prefix to the given secret name to prevent name collisions
// in namespaces where multiple service providers operate.
func prefixSecretName(secretName string) (string, error) {
	if secretName == "" {
		return "", nil
	}
	return ctrlutils.ShortenToXCharacters(fmt.Sprintf("%s%s", secretNamePrefix, secretName), ctrlutils.K8sMaxNameLength)
}
