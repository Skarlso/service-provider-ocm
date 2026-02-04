package runtime

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	"github.com/openmcp-project/openmcp-operator/lib/clusteraccess"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// ServiceProviderReconciler implements any business logic required to manage ServiceProviderAPI objects
type ServiceProviderReconciler[T ServiceProviderAPI, PC ProviderConfig] interface {
	// CreateOrUpdate is called on every add or update event
	CreateOrUpdate(ctx context.Context, obj T, pc PC, clusters ClusterContext) (ctrl.Result, error)
	// Delete is called on every delete event
	Delete(ctx context.Context, obj T, pc PC, clusters ClusterContext) (ctrl.Result, error)
}

// ClusterContext provides access to request-scoped clusters.
// These clusters include the managed control plane and workload clusters associated with a specific reconcile request.
// (Static clusters like the platform and onboarding clusters are provided to the reconciler when it is initialized.)
//
// More info on the deployment model:
// https://openmcp-project.github.io/docs/about/design/service-provider#deployment-model
type ClusterContext struct {
	// MCPCluster is the managed control plane that belongs to the current reconcile request
	MCPCluster *clusters.Cluster
	// WorkloadCluster is the workload cluster that belongs the current reconcile request
	WorkloadCluster *clusters.Cluster
}

// ServiceProviderAPI represents the end-user facing onboarding api type
type ServiceProviderAPI interface {
	client.Object
	ServiceProviderAPIStatus
	Finalizer() string
}

// ServiceProviderAPIStatus represents the common status contract of ServiceProviderAPI types
type ServiceProviderAPIStatus interface {
	// GetStatus returns the status object
	GetStatus() any
	// GetConditions returns the status object
	GetConditions() *[]metav1.Condition
	// SetPhase sets Status.Phase
	SetPhase(string)
	// SetObservedGeneration sets Status.ObservedGeneration
	SetObservedGeneration(int64)
}

// ProviderConfig represents the config for platform operators
// The ProviderConfig is passed to the DomainServiceReconcile to reconcile APIObjects
type ProviderConfig interface {
	client.Object
	// PollIntveral can be used to periodically requeue, preventing managed objects
	// from drifting on the target cluster.  Return 0 if not required.
	PollInterval() time.Duration
}

// SPReconciler implements a generic reconcile loop to separate platform
// and service provider developer space.
type SPReconciler[T ServiceProviderAPI, PC ProviderConfig] struct {
	// platformCluster represents the platform cluster of the v2 architecture
	platformCluster *clusters.Cluster
	// onboardingCluster represents the onboarding cluster of the v2 architecture
	onboardingCluster *clusters.Cluster
	// clusterAccessReconciler reconciles access to MCP and workload clusters
	clusterAccessReconciler clusteraccess.Reconciler
	// serviceProviderReonciler reconciles the end-user facing onboarding API of a service provider
	serviceProviderReconciler ServiceProviderReconciler[T, PC]
	// providerConfig represents the platform operator facing platform API of a service provider
	providerConfig atomic.Pointer[PC]
	// withWorkloadCluster defines whether a service provider requires access to a workload cluster
	withWorkloadCluster bool
	// emptyObj creates an empty object of the api type
	emptyObj func() T
}

// NewSPReconciler creates a reconciler instance for the given types.
func NewSPReconciler[T ServiceProviderAPI, PC ProviderConfig](emptyObj func() T) *SPReconciler[T, PC] {
	return &SPReconciler[T, PC]{
		emptyObj: emptyObj,
	}
}

// WithPlatformCluster set the platform cluster.
func (r *SPReconciler[T, PC]) WithPlatformCluster(c *clusters.Cluster) *SPReconciler[T, PC] {
	r.platformCluster = c
	return r
}

// WithOnboardingCluster set the onboarding cluster.
func (r *SPReconciler[T, PC]) WithOnboardingCluster(c *clusters.Cluster) *SPReconciler[T, PC] {
	r.onboardingCluster = c
	return r
}

// WithClusterAccessReconciler sets the cluster access reconciler.
func (r *SPReconciler[T, PC]) WithClusterAccessReconciler(car clusteraccess.Reconciler) *SPReconciler[T, PC] {
	r.clusterAccessReconciler = car
	return r
}

// WithServiceProviderReconciler sets the service provider reconciler.
func (r *SPReconciler[T, PC]) WithServiceProviderReconciler(dsr ServiceProviderReconciler[T, PC]) *SPReconciler[T, PC] {
	r.serviceProviderReconciler = dsr
	return r
}

// WithWorkloadCluster sets if the service provider reconciler requests a workload cluster
func (r *SPReconciler[T, PC]) WithWorkloadCluster(b bool) *SPReconciler[T, PC] {
	r.withWorkloadCluster = b
	return r
}

// Reconcile orchestrates platform and DomainServiceReconciler logic to reconcile APIObjects
func (r *SPReconciler[T, PC]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := logf.FromContext(ctx)
	// common reconciler logic including get obj, providerconfig, mcp/workload access
	obj := r.emptyObj()
	if err := r.onboardingCluster.Client().Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	oldObj := obj.DeepCopyObject().(T)
	providerConfig := r.providerConfig.Load()
	if providerConfig == nil {
		StatusProgressing(obj, "ReconcileError", "No ProviderConfig found")
		r.updateStatus(ctx, obj, oldObj)
		return ctrl.Result{}, errors.New("provider config missing")
	}
	providerConfigCopy := (*providerConfig).DeepCopyObject().(PC)
	// core crud
	deleted := !obj.GetDeletionTimestamp().IsZero()
	var res ctrl.Result
	var err error
	if deleted {
		res, err = r.delete(ctx, obj, providerConfigCopy)
	} else {
		res, err = r.createOrUpdate(ctx, obj, providerConfigCopy)
		r.updateStatus(ctx, obj, oldObj)
	}
	// return based on result/err
	if err != nil {
		l.Error(err, "reconcile failed")
		return ctrl.Result{}, err
	}
	if res.RequeueAfter > 0 {
		return res, nil
	}
	// fallback to poll interval to prevent 'managed service' drift
	return ctrl.Result{
		RequeueAfter: providerConfigCopy.PollInterval(),
	}, nil
}

func (r *SPReconciler[T, PC]) updateStatus(ctx context.Context, newObj T, oldObj T) {
	if equality.Semantic.DeepEqual(oldObj.GetStatus(), newObj.GetStatus()) {
		return
	}
	if err := r.onboardingCluster.Client().Status().Patch(ctx, newObj, client.MergeFrom(oldObj)); err != nil {
		l := logf.FromContext(ctx)
		l.Error(err, "Patch status failed")
	}
}

// delete eventually invokes the domain delete logic of a service provider and is the place to implement
// common logic that should be abstracted away from a service provider developer like handling cluster access.
func (r *SPReconciler[T, PC]) delete(ctx context.Context, obj T, pc PC) (ctrl.Result, error) {
	l := logf.FromContext(ctx)
	oldObj := obj.DeepCopyObject().(T)

	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
	accessRequestsInDeletion, err := r.areAccessRequestsInDeletion(ctx, req)
	if err != nil {
		l.Error(err, "failed to check access requests in deletion")
		return reconcile.Result{}, err
	}
	if !accessRequestsInDeletion {
		clusters, res, err := r.clusters(ctx, req)
		if err != nil {
			StatusProgressing(obj, "ReconcileError", "cluster setup error")
			return ctrl.Result{}, err
		}
		if res.RequeueAfter > 0 {
			StatusProgressing(obj, "Reconciling", "clusters being setup")
			return res, nil
		}
		res, err = r.serviceProviderReconciler.Delete(ctx, obj, pc, clusters)
		r.updateStatus(ctx, obj, oldObj)
		if err != nil {
			return ctrl.Result{}, err
		}
		if res.RequeueAfter > 0 {
			return res, nil
		}
	}
	// remove cluster access
	res, err := r.clusterAccessReconciler.ReconcileDelete(ctx, req)
	if err != nil {
		return ctrl.Result{}, err
	}
	// make sure to not drop the object before cleanup has been done
	if res.RequeueAfter > 0 {
		return res, nil
	}
	// remove finalizer
	controllerutil.RemoveFinalizer(obj, obj.Finalizer())
	if err := r.onboardingCluster.Client().Update(ctx, obj); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// createOrUpdate eventually invokes the domain createOrUpdate logic of a service provider and is the place to implement
// common logic that should be abstracted away from a service provider developer like handling cluster access.
func (r *SPReconciler[T, PC]) createOrUpdate(ctx context.Context, obj T, pc PC) (ctrl.Result, error) {
	if _, err := controllerutil.CreateOrUpdate(ctx, r.onboardingCluster.Client(), obj, func() error {
		controllerutil.AddFinalizer(obj, obj.Finalizer())
		return nil
	}); err != nil {
		return ctrl.Result{}, err
	}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
	clusters, res, err := r.clusters(ctx, req)
	if err != nil {
		return ctrl.Result{}, err
	}
	if res.RequeueAfter > 0 {
		return res, nil
	}
	return r.serviceProviderReconciler.CreateOrUpdate(ctx, obj, pc, clusters)
}

// areAccessRequestsInDeletion determines if the access requests for a reconcile request are in deletion.
// It returns true if any access requests (mcp, workload) is deleted or has a deletion timestamp.
// It is used to prevent renewing cluster access when deleting an ServiceProviderAPI object.
func (r *SPReconciler[T, PC]) areAccessRequestsInDeletion(ctx context.Context, req ctrl.Request) (bool, error) {
	accessRequest, err := r.clusterAccessReconciler.MCPAccessRequest(ctx, req)
	if apierrors.IsNotFound(err) || accessRequest.DeletionTimestamp != nil {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if r.withWorkloadCluster {
		accessRequest, err = r.clusterAccessReconciler.WorkloadAccessRequest(ctx, req)
		if apierrors.IsNotFound(err) || accessRequest.DeletionTimestamp != nil {
			return true, nil
		}
		if err != nil {
			return false, err
		}
	}
	return false, nil
}

// clusters returns any request scoped cluster that a servicer provider developer might want to access in order
// to delivery its service.
func (r *SPReconciler[T, PC]) clusters(ctx context.Context, req ctrl.Request) (ClusterContext, ctrl.Result, error) {
	l := logf.FromContext(ctx)
	clusters := ClusterContext{}
	res, err := r.clusterAccessReconciler.Reconcile(ctx, req)
	if err != nil {
		return clusters, ctrl.Result{}, err
	}
	if res.RequeueAfter > 0 {
		return clusters, res, nil
	}
	mcpCluster, err := r.clusterAccessReconciler.MCPCluster(ctx, req)
	if err != nil {
		return clusters, ctrl.Result{}, err
	}
	if mcpCluster == nil {
		return clusters, res, errors.New("mcp access missing")
	}
	clusters.MCPCluster = mcpCluster
	if r.withWorkloadCluster {
		workloadCluster, err := r.clusterAccessReconciler.WorkloadCluster(ctx, req)
		if err != nil {
			l.Error(err, "workload cluster access error")
			return clusters, ctrl.Result{}, err
		}
		if workloadCluster == nil {
			return clusters, res, errors.New("workload cluster access missing")
		}
		clusters.WorkloadCluster = workloadCluster
	}
	return clusters, res, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SPReconciler[T, PC]) SetupWithManager(mgr ctrl.Manager, name string, providerConfigUpdates chan event.GenericEvent) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(r.emptyObj()).
		// sets up reconciles whenever provider config controller sends update events
		WatchesRawSource(
			source.Channel(
				providerConfigUpdates,
				handler.EnqueueRequestsFromMapFunc(
					func(ctx context.Context, obj client.Object) []reconcile.Request {
						// update cached provider config
						if obj != nil {
							c := obj.DeepCopyObject().(PC)
							r.providerConfig.Store(&c)
						} else {
							r.providerConfig.Store(nil)
						}
						// reconcile all existing objects
						var list unstructured.UnstructuredList
						gvk := r.emptyObj().GetObjectKind().GroupVersionKind()
						list.SetGroupVersionKind(gvk)
						if err := r.onboardingCluster.Client().List(ctx, &list); err != nil {
							return nil
						}
						reqs := make([]reconcile.Request, len(list.Items))
						for i := range list.Items {
							reqs[i] = reconcile.Request{
								NamespacedName: client.ObjectKeyFromObject(&list.Items[i]),
							}
						}
						return reqs
					},
				)),
		).
		Named(name).
		Complete(r)
}
