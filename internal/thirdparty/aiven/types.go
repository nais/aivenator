// +kubebuilder:object:generate=true
package aiven

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// Minimal typed stubs for aiven.io/v1alpha1 CRDs.
// We read metadata + status.state to verify the instance is running.

// ServiceStatus holds the subset of status we need from Aiven CRDs.
type ServiceStatus struct {
	State string `json:"state,omitempty"`
}

// +kubebuilder:object:root=true
type OpenSearch struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Status            ServiceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type OpenSearchList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OpenSearch `json:"items"`
}

// +kubebuilder:object:root=true
type Valkey struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Status            ServiceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ValkeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Valkey `json:"items"`
}
