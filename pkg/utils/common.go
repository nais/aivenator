package utils

import (
	"fmt"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

func Expired(expiredAt time.Time) bool {
	return time.Now().After(expiredAt)
}

func ParseTimestamp(expiresAt string, errs *[]error) time.Time {
	expired, err := Parse(expiresAt)
	if err != nil {
		*errs = append(*errs, err)
	}
	return expired
}

func Parse(expiresAt string) (time.Time, error) {
	return time.Parse(time.RFC3339, expiresAt)
}

func GetGVK(scheme *runtime.Scheme, obj runtime.Object) (*schema.GroupVersionKind, error) {
	kinds, unversioned, err := scheme.ObjectKinds(obj)
	if err != nil {
		return nil, err
	}
	if unversioned {
		return nil, fmt.Errorf("object %v is unversioned", obj)
	}
	if len(kinds) == 0 {
		return nil, fmt.Errorf("no kinds registered for %v", obj)
	}
	return &kinds[0], nil
}

func MakeOwnerReference(in client.Object) (metav1.OwnerReference, error) {
	metaAccessor, err := meta.Accessor(in)
	if err != nil {
		return metav1.OwnerReference{}, err
	}
	typeAccessor, err := meta.TypeAccessor(in)
	if err != nil {
		return metav1.OwnerReference{}, err
	}

	return metav1.OwnerReference{
		APIVersion: typeAccessor.GetAPIVersion(),
		Kind:       typeAccessor.GetKind(),
		Name:       metaAccessor.GetName(),
		UID:        metaAccessor.GetUID(),
	}, nil
}
