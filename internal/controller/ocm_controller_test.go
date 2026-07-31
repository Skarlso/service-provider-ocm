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
	"errors"
	"testing"

	"github.com/fluxcd/pkg/apis/meta"
	"github.com/openmcp-project/controller-utils/pkg/clusters"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	apiv1alpha1 "github.com/open-component-model/service-provider-ocm/api/v1alpha1"
	spruntime "github.com/open-component-model/service-provider-ocm/pkg/runtime"
)

func TestResourceStatus(t *testing.T) {
	tests := []struct {
		name       string
		conditions []metav1.Condition
		wantPhase  apiv1alpha1.InstancePhase
		wantMsg    string
	}{
		{
			name:       "no conditions",
			conditions: nil,
			wantPhase:  apiv1alpha1.Progressing,
			wantMsg:    "",
		},
		{
			name: "ready true",
			conditions: []metav1.Condition{
				{
					Type:    meta.ReadyCondition,
					Status:  metav1.ConditionTrue,
					Message: "stored artifact for revision abc123",
				},
			},
			wantPhase: apiv1alpha1.Ready,
			wantMsg:   "",
		},
		{
			name: "ready false carries message",
			conditions: []metav1.Condition{
				{
					Type:    meta.ReadyCondition,
					Status:  metav1.ConditionFalse,
					Message: "install retries exhausted",
				},
			},
			wantPhase: apiv1alpha1.Progressing,
			wantMsg:   "install retries exhausted",
		},
		{
			name: "ready unknown carries message",
			conditions: []metav1.Condition{
				{
					Type:    meta.ReadyCondition,
					Status:  metav1.ConditionUnknown,
					Message: "reconciliation in progress",
				},
			},
			wantPhase: apiv1alpha1.Progressing,
			wantMsg:   "reconciliation in progress",
		},
		{
			name: "ready missing among other conditions",
			conditions: []metav1.Condition{
				{
					Type:   meta.StalledCondition,
					Status: metav1.ConditionFalse,
				},
			},
			wantPhase: apiv1alpha1.Progressing,
			wantMsg:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			phase, msg := resourceStatus(tc.conditions)
			if phase != tc.wantPhase {
				t.Errorf("phase: got %q, want %q", phase, tc.wantPhase)
			}
			if msg != tc.wantMsg {
				t.Errorf("message: got %q, want %q", msg, tc.wantMsg)
			}
		})
	}
}

// fakeMCPCluster builds a cluster whose client answers a Repository list with the
// given behaviour: repoCount items, or listErr if non-nil.
func fakeMCPCluster(t *testing.T, repoCount int, listErr error) *clusters.Cluster {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				ul, ok := list.(*unstructured.UnstructuredList)
				if !ok {
					return c.List(ctx, list, opts...)
				}
				if listErr != nil {
					return listErr
				}
				for i := 0; i < repoCount; i++ {
					u := unstructured.Unstructured{}
					u.SetGroupVersionKind(schema.GroupVersionKind{Group: ocmAPIGroup, Version: ocmAPIVersion, Kind: "Repository"})
					u.SetName("repo-" + string(rune('a'+i)))
					ul.Items = append(ul.Items, u)
				}
				return nil
			},
		}).
		Build()
	return clusters.NewTestClusterFromClient("mcp", fakeClient)
}

func TestCountRepositories(t *testing.T) {
	tests := []struct {
		name      string
		repoCount int
		listErr   error
		want      int
		wantErr   bool
	}{
		{name: "none present", repoCount: 0, want: 0},
		{name: "some present", repoCount: 3, want: 3},
		{
			name:    "ocm CRD not installed -> treated as none",
			listErr: &apimeta.NoKindMatchError{GroupKind: schema.GroupKind{Group: ocmAPIGroup, Kind: "Repository"}},
			want:    0,
		},
		{
			name:    "unexpected error propagates",
			listErr: errors.New("boom"),
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &OCMReconciler{}
			got, err := r.countRepositories(context.Background(), fakeMCPCluster(t, tc.repoCount, tc.listErr))
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDelete_BlockedByRepositories(t *testing.T) {
	obj := &apiv1alpha1.OCM{
		ObjectMeta: metav1.ObjectMeta{Name: "mcp-01", Namespace: "tenant"},
	}
	r := &OCMReconciler{}
	clusterCtx := spruntime.ClusterContext{MCPCluster: fakeMCPCluster(t, 2, nil)}

	res, err := r.Delete(context.Background(), obj, &apiv1alpha1.ProviderConfig{}, clusterCtx)
	require.NoError(t, err)

	// Deletion is held: we requeue instead of tearing down and dropping the finalizer.
	assert.Equal(t, deletionBlockedRequeue, res.RequeueAfter)
	assert.Equal(t, spruntime.StatusPhaseTerminating, obj.Status.Phase)

	cond := apimeta.FindStatusCondition(obj.Status.Conditions, spruntime.ServiceProviderConditionReady)
	require.NotNil(t, cond)
	assert.Contains(t, cond.Message, "deletion blocked")
	assert.Contains(t, cond.Message, "2 ocm Repository")
}
