package runtime

import (
	"context"
	"testing"

	"github.com/go-logr/zapr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	apiconst "github.com/openmcp-project/openmcp-operator/api/constants"
)

func TestSPReconciler_Reconcile_IgnoreAnnotation(t *testing.T) {
	obj := &fakeApiImpl{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "obj-1",
			Namespace: testNamespaceName,
			Annotations: map[string]string{
				apiconst.OperationAnnotation: apiconst.OperationAnnotationValueIgnore,
			},
		},
	}
	r := NewSPReconciler[*fakeApiImpl, *fakeProviderConfigImpl](func() *fakeApiImpl {
		return &fakeApiImpl{}
	}).
		WithOnboardingCluster(createFakeCluster(t, "onboarding", obj))

	got, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "obj-1", Namespace: testNamespaceName},
	})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, got)
}

func TestSPReconciler_Reconcile_ReconcileAnnotationConsumed(t *testing.T) {
	obj := &fakeApiImpl{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "obj-1",
			Namespace: testNamespaceName,
			Annotations: map[string]string{
				apiconst.OperationAnnotation: apiconst.OperationAnnotationValueReconcile,
				"keep.me/annotation":         "value",
			},
		},
	}
	onboardingCluster := createFakeCluster(t, "onboarding", obj)
	r := NewSPReconciler[*fakeApiImpl, *fakeProviderConfigImpl](func() *fakeApiImpl {
		return &fakeApiImpl{}
	}).
		WithOnboardingCluster(onboardingCluster)

	// Reconcile stops later at the missing ProviderConfig, but the reconcile
	// annotation must already have been consumed by then.
	_, _ = r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "obj-1", Namespace: testNamespaceName},
	})

	got := &fakeApiImpl{}
	require.NoError(t, onboardingCluster.Client().Get(context.Background(),
		types.NamespacedName{Name: "obj-1", Namespace: testNamespaceName}, got))
	_, hasOperation := got.GetAnnotations()[apiconst.OperationAnnotation]
	assert.False(t, hasOperation, "reconcile operation annotation should be removed")
	assert.Equal(t, "value", got.GetAnnotations()["keep.me/annotation"], "unrelated annotations must be preserved")
}

func TestSPReconciler_enqueueAllObjects(t *testing.T) {
	tests := []struct {
		name              string
		onboardingCluster *clusters.Cluster
		wantErrorMessage  string
		want              []reconcile.Request
	}{
		{
			name: "expect reconcile requests",
			onboardingCluster: createFakeClusterWithUnstructuredList(t, "onboarding", []client.Object{
				&fakeApiImpl{ObjectMeta: metav1.ObjectMeta{Name: "obj-1", Namespace: testNamespaceName}},
				&fakeApiImpl{ObjectMeta: metav1.ObjectMeta{Name: "obj-2", Namespace: testNamespaceName}},
			}),
			want: []reconcile.Request{
				{NamespacedName: types.NamespacedName{Name: "obj-1", Namespace: testNamespaceName}},
				{NamespacedName: types.NamespacedName{Name: "obj-2", Namespace: testNamespaceName}},
			},
		},
		{
			name:              "expect gvk error log without registering fake api scheme",
			onboardingCluster: clusters.NewTestClusterFromClient("onboarding", fake.NewClientBuilder().Build()),
			wantErrorMessage:  "failed to retrieve gvk",
			want:              nil,
		},
	}
	for _, tt := range tests {
		core, observedLogs := observer.New(zap.ErrorLevel)
		testContext := log.IntoContext(context.Background(), zapr.NewLogger(zap.New(core)))
		t.Run(tt.name, func(t *testing.T) {
			r := NewSPReconciler[*fakeApiImpl, *fakeProviderConfigImpl](func() *fakeApiImpl {
				return &fakeApiImpl{}
			})
			r.onboardingCluster = tt.onboardingCluster
			got := r.enqueueAllObjects(testContext)
			if len(got) == 0 {
				logs := observedLogs.All()
				require.Len(t, logs, 1)
				assert.Equal(t, zap.ErrorLevel, logs[0].Level)
				assert.Equal(t, tt.wantErrorMessage, logs[0].Message)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}
