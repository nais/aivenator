package aiven

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var SchemeGroupVersion = schema.GroupVersion{Group: "aiven.io", Version: "v1alpha1"}

func AddToScheme(s *runtime.Scheme) error {
	s.AddKnownTypes(
		SchemeGroupVersion,
		&OpenSearch{}, &OpenSearchList{},
		&Valkey{}, &ValkeyList{},
	)
	return nil
}
