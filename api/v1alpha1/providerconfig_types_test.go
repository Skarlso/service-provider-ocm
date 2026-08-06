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

package v1alpha1_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/open-component-model/service-provider-ocm/api/v1alpha1"
)

func providerConfig(versions ...v1alpha1.OCMVersion) *v1alpha1.ProviderConfig {
	return &v1alpha1.ProviderConfig{
		Spec: v1alpha1.ProviderConfigSpec{Versions: versions},
	}
}

func TestResolveVersion(t *testing.T) {
	// The tenant facing version is matched verbatim and need not equal the chart tag:
	// "v0.12.0" here deliberately maps onto the unprefixed chart version "0.12.0".
	prefixed := v1alpha1.OCMVersion{
		Version:         "v0.12.0",
		ChartVersion:    "0.12.0",
		ChartURL:        new("oci://ghcr.io/example/chart"),
		ChartPullSecret: "regcred",
	}
	plain := v1alpha1.OCMVersion{Version: "0.11.0", ChartVersion: "0.11.0"}

	tests := []struct {
		name      string
		config    *v1alpha1.ProviderConfig
		requested string
		want      v1alpha1.OCMVersion
		wantErr   string
	}{
		{
			name:      "resolves a v-prefixed version onto its chart version",
			config:    providerConfig(prefixed, plain),
			requested: "v0.12.0",
			want:      prefixed,
		},
		{
			name:      "resolves an unprefixed version",
			config:    providerConfig(prefixed, plain),
			requested: "0.11.0",
			want:      plain,
		},
		{
			name:      "match is verbatim: a v prefix is not inferred",
			config:    providerConfig(prefixed),
			requested: "0.12.0",
			wantErr:   `"0.12.0", available versions are: v0.12.0`,
		},
		{
			name:      "unknown version lists what is on offer",
			config:    providerConfig(prefixed, plain),
			requested: "9.9.9",
			wantErr:   `"9.9.9", available versions are: v0.12.0, 0.11.0`,
		},
		{
			name:      "empty version list",
			config:    providerConfig(),
			requested: "0.12.0",
			wantErr:   "the provider config offers no versions",
		},
		{
			name:      "nil provider config",
			config:    nil,
			requested: "0.12.0",
			wantErr:   "no provider config is configured",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.config.ResolveVersion(tc.requested)
			if tc.wantErr != "" {
				require.Error(t, err)
				// Callers branch on this sentinel to tell user input apart from real failures.
				assert.ErrorIs(t, err, v1alpha1.ErrVersionNotAvailable)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestOCMVersionGetChartURL(t *testing.T) {
	tests := []struct {
		name string
		url  *string
		want string
	}{
		{name: "unset falls back to the default", url: nil, want: v1alpha1.DefaultChartURL},
		{name: "empty falls back to the default", url: new(""), want: v1alpha1.DefaultChartURL},
		{name: "scheme is preserved", url: new("oci://example.com/chart"), want: "oci://example.com/chart"},
		{name: "missing scheme is added", url: new("example.com/chart"), want: "oci://example.com/chart"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, v1alpha1.OCMVersion{ChartURL: tc.url}.GetChartURL())
		})
	}
}

func TestPollInterval(t *testing.T) {
	// Nil-safe on both the receiver and the field: the CRD default does not apply to
	// objects built in Go, and a missing interval must not panic the reconcile loop.
	assert.Equal(t, v1alpha1.DefaultPollInterval, (*v1alpha1.ProviderConfig)(nil).PollInterval())
	assert.Equal(t, v1alpha1.DefaultPollInterval, providerConfig().PollInterval())

	pc := providerConfig()
	pc.Spec.PollInterval = &metav1.Duration{Duration: 5 * time.Minute}
	assert.Equal(t, 5*time.Minute, pc.PollInterval())
}
