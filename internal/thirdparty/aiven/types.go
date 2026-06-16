// +kubebuilder:object:generate=true
package aiven

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// Minimal typed stubs for aiven.io/v1alpha1 CRDs.
// Only metadata is needed — we check existence, not spec/status.

// +kubebuilder:object:root=true
type OpenSearch struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
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
}

// +kubebuilder:object:root=true
type ValkeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Valkey `json:"items"`
}
