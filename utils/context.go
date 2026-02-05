package utils

import (
	"context"

	"github.com/fluxcd/pkg/apis/meta"
	corev1 "k8s.io/api/core/v1"
)

type fluxKubeconfigKey struct{}

func WithFluxKubeconfigRef(ctx context.Context, ref *corev1.SecretReference) context.Context {
	return context.WithValue(ctx, fluxKubeconfigKey{}, &meta.KubeConfigReference{
		SecretRef: &meta.SecretKeyReference{
			Name: ref.Name,
			Key:  "kubeconfig",
		},
	})
}

func FluxKubeconfigRef(ctx context.Context) *meta.KubeConfigReference {
	return ctx.Value(fluxKubeconfigKey{}).(*meta.KubeConfigReference)
}
