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
	"encoding/json"
	"fmt"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"github.com/fluxcd/pkg/apis/meta"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	clustersv1alpha1 "github.com/openmcp-project/openmcp-operator/api/clusters/v1alpha1"
	libutils "github.com/openmcp-project/openmcp-operator/lib/utils"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	"github.com/openmcp-project/openmcp-operator/lib/clusteraccess"

	apiv1alpha1 "github.com/open-component-model/service-provider-ocm/api/v1alpha1"
	spruntime "github.com/open-component-model/service-provider-ocm/pkg/runtime"
)

const (
	// HelmReleaseName is the name of the helm releases. Wow, such comment, much useful.
	HelmReleaseName = "helm-release"
	// OCIRepositoryName is the name of the oci repository. Wow, such comment, much useful.
	OCIRepositoryName = "oci-repository"
	requestSuffixMCP  = "--mcp"
)

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
func (r *OCMReconciler) CreateOrUpdate(ctx context.Context, svcobj *apiv1alpha1.OCM, _ *apiv1alpha1.ProviderConfig, clusters spruntime.ClusterContext) (ctrl.Result, error) {
	l := logf.FromContext(ctx)
	l.Info("Reconciling OCM resource", "name", svcobj.Name)
	spruntime.StatusProgressing(svcobj, "Reconciling", "Reconcile in progress")
	tenantNamespace, err := libutils.StableMCPNamespace(svcobj.Name, svcobj.Namespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to determine stable namespace for OCM instance: %w", err)
	}

	l.Info("checking tenantNamespace", "namespace", tenantNamespace)

	if err := r.createOrUpdateOCIRepository(ctx, svcobj, clusters, tenantNamespace); err != nil {
		spruntime.StatusFailed(svcobj, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to reconcile OCI Repository: %w", err)
	}
	if err := r.createOrUpdateHelmRelease(ctx, tenantNamespace, svcobj.Name); err != nil {
		spruntime.StatusFailed(svcobj, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to reconcile HelmRelease: %w", err)
	}

	l.Info("Done reconciling OCM resource", "name", svcobj.Name)

	spruntime.StatusReady(svcobj)
	return ctrl.Result{}, nil
}

// Delete is called on every delete event
func (r *OCMReconciler) Delete(ctx context.Context, obj *apiv1alpha1.OCM, _ *apiv1alpha1.ProviderConfig, _ spruntime.ClusterContext) (ctrl.Result, error) {
	spruntime.StatusTerminating(obj)

	tenantNamespace, err := libutils.StableMCPNamespace(obj.Name, obj.Namespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to determine stable namespace for OCM instance: %w", err)
	}

	var objects []client.Object
	ociRepository := createOciRepository("oci://ghcr.io/open-component-model/charts/ocm-k8s-toolkit", "0.0.0-0a2b7a3", tenantNamespace)
	objects = append(objects, ociRepository)
	helmRelease, err := r.createHelmRelease(ctx, tenantNamespace, obj.Name)
	if err != nil {
		spruntime.StatusFailed(obj, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to create helm release: %w", err)
	}
	objects = append(objects, helmRelease)

	objectsStillExist := false
	for _, managedObj := range objects {
		if err := r.PlatformCluster.Client().Delete(ctx, managedObj); client.IgnoreNotFound(err) != nil {
			spruntime.StatusFailed(obj, err.Error())
			return ctrl.Result{}, fmt.Errorf("delete object failed: %w", err)
		}
		// we ignore any other error because we assume that if deleting worked, getting should not fail with anything other than
		// not found.
		if err := r.PlatformCluster.Client().Get(ctx, client.ObjectKeyFromObject(managedObj), managedObj); err != nil {
			objectsStillExist = true
		}
	}

	if objectsStillExist {
		return ctrl.Result{
			RequeueAfter: time.Second * 10,
		}, nil
	}

	spruntime.StatusReady(obj)
	return ctrl.Result{}, nil
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

func (r *OCMReconciler) createOrUpdateOCIRepository(ctx context.Context, svcobj *apiv1alpha1.OCM, _ spruntime.ClusterContext, namespace string) error {
	ociRepository := createOciRepository(svcobj.Spec.URL, svcobj.Spec.Version, namespace)
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
		return err
	}

	return nil
}

func (r *OCMReconciler) createOrUpdateHelmRelease(ctx context.Context, namespace, objectName string) error {
	helmRelease, err := r.createHelmRelease(ctx, namespace, objectName)
	if err != nil {
		return fmt.Errorf("failed to create helm release: %w", err)
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
		return err
	}

	return nil
}

func createOciRepository(url, version, namespace string) *sourcev1.OCIRepository {
	return &sourcev1.OCIRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      OCIRepositoryName,
			Namespace: namespace,
		},
		Spec: sourcev1.OCIRepositorySpec{
			Interval: metav1.Duration{Duration: time.Minute},
			URL:      url,
			Reference: &sourcev1.OCIRepositoryRef{
				Tag: version,
			},
		},
	}
}

func (r *OCMReconciler) createHelmRelease(ctx context.Context, namespace, objectName string) (*helmv2.HelmRelease, error) {
	fluxConfigRef, err := r.getMcpFluxConfig(ctx, namespace, objectName)
	if err != nil {
		return nil, fmt.Errorf("failed to get FluxConfig: %w", err)
	}

	values := make(map[string]interface{})
	values["manager"] = map[string]interface{}{
		"concurrency": map[string]interface{}{
			"resource": 21,
		},
		"logging": map[string]interface{}{
			"level": "debug",
		},
	}
	content, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}

	return &helmv2.HelmRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      HelmReleaseName,
			Namespace: namespace,
		},
		Spec: helmv2.HelmReleaseSpec{
			Interval:         metav1.Duration{Duration: time.Minute},
			TargetNamespace:  metav1.NamespaceDefault,
			StorageNamespace: metav1.NamespaceDefault,
			ChartRef: &helmv2.CrossNamespaceSourceReference{
				Kind:      "OCIRepository",
				Name:      OCIRepositoryName,
				Namespace: namespace,
			},
			Values: &apiextensionsv1.JSON{Raw: content},
			KubeConfig: &meta.KubeConfigReference{
				SecretRef: fluxConfigRef,
			},
		},
	}, nil
}
