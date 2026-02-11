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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OCMSpec defines the desired state of OCM
type OCMSpec struct {
	// URL defines the url from where to fetch the helm charts to install the ocm-k8s-toolkit.
	URL string `json:"url"`
	// Version defines the version to fetch.
	Version string `json:"version"`
}

// OCMStatus defines the observed state of OCM.
type OCMStatus struct {
	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the OCM resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// ObservedGeneration is the generation of this resource that was last reconciled by the controller.
	ObservedGeneration int64 `json:"observedGeneration"`
	// Phase is the current phase of the resource.
	Phase string `json:"phase"`
}

// OCM is the Schema for the ocms API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=`.status.phase`,name="Phase",type=string
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:metadata:labels="openmcp.cloud/cluster=onboarding"
type OCM struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of OCM
	// +required
	Spec OCMSpec `json:"spec"`

	// status defines the observed state of OCM
	// +optional
	Status OCMStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// OCMList contains a list of OCM
type OCMList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OCM `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OCM{}, &OCMList{})
}

// Finalizer returns the finalizer string for the OCM resource
func (o *OCM) Finalizer() string {
	return GroupVersion.Group + "/finalizer"
}

// GetStatus returns the status of the OCM resource
func (o *OCM) GetStatus() any {
	return o.Status
}

// GetConditions returns the conditions of the OCM resource
func (o *OCM) GetConditions() *[]metav1.Condition {
	return &o.Status.Conditions
}

// SetPhase sets the phase of the OCM resource status
func (o *OCM) SetPhase(phase string) {
	o.Status.Phase = phase
}

// SetObservedGeneration sets the observed generation of the OCM resource
func (o *OCM) SetObservedGeneration(gen int64) {
	o.Status.ObservedGeneration = gen
}
