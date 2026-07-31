package runtime

import (
	"context"
	"testing"
	"time"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
)

const testNamespaceName = "test-namespace"

var testGV = schema.GroupVersion{Group: "openmcp.test", Version: "v1"}

func createFakeCluster(t *testing.T, id string, clusterObjects ...client.Object) *clusters.Cluster {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = apiextv1.AddToScheme(scheme)
	scheme.AddKnownTypes(testGV, &fakeApiImpl{}, &fakeProviderConfigImpl{})

	fakeClient := fake.NewClientBuilder().WithObjects(clusterObjects...).WithScheme(scheme).Build()
	return clusters.NewTestClusterFromClient(id, fakeClient)
}

// createFakeClusterWithUnstructuredList creates a fake cluster whose client supports
// listing unstructured objects by intercepting List calls and populating the result
// from the given objects.
func createFakeClusterWithUnstructuredList(t *testing.T, id string, objs []client.Object) *clusters.Cluster {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	scheme.AddKnownTypes(testGV, &fakeApiImpl{}, &fakeProviderConfigImpl{})

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if ul, ok := list.(*unstructured.UnstructuredList); ok {
					for _, obj := range objs {
						u := unstructured.Unstructured{}
						u.SetName(obj.GetName())
						u.SetNamespace(obj.GetNamespace())
						ul.Items = append(ul.Items, u)
					}
					return nil
				}
				return c.List(ctx, list, opts...)
			},
		}).
		Build()
	return clusters.NewTestClusterFromClient(id, fakeClient)
}

var _ ServiceProviderAPI = &fakeApiImpl{}

type fakeApiImpl struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	conditions        []metav1.Condition
}

func (f *fakeApiImpl) DeepCopyObject() runtime.Object {
	return &fakeApiImpl{
		TypeMeta:   f.TypeMeta,
		ObjectMeta: *f.DeepCopy(),
		conditions: append([]metav1.Condition(nil), f.conditions...),
	}
}

func (f *fakeApiImpl) Finalizer() string {
	return "fakeFinalizer"
}

func (f *fakeApiImpl) GetConditions() *[]metav1.Condition {
	return &f.conditions
}

func (f *fakeApiImpl) GetStatus() any {
	return nil
}

func (f *fakeApiImpl) SetPhase(string) {}

func (f *fakeApiImpl) SetObservedGeneration(int64) {}

var _ ProviderConfig = &fakeProviderConfigImpl{}

type fakeProviderConfigImpl struct {
	metav1.TypeMeta
	metav1.ObjectMeta
	FakePollInterval time.Duration
}

func (f *fakeProviderConfigImpl) DeepCopyObject() runtime.Object {
	return f
}

func (f *fakeProviderConfigImpl) PollInterval() time.Duration {
	return f.FakePollInterval
}
