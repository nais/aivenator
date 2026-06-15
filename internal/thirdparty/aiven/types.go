package aiven

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Minimal typed stubs for aiven.io/v1alpha1 CRDs.
// Only metadata is needed — we check existence, not spec/status.

type OpenSearch struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
}

type OpenSearchList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OpenSearch `json:"items"`
}

type Valkey struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
}

type ValkeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Valkey `json:"items"`
}

func (o *OpenSearch) DeepCopyObject() runtime.Object     { c := *o; return &c }
func (o *OpenSearchList) DeepCopyObject() runtime.Object { c := *o; return &c }
func (v *Valkey) DeepCopyObject() runtime.Object         { c := *v; return &c }
func (v *ValkeyList) DeepCopyObject() runtime.Object     { c := *v; return &c }
