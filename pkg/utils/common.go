package utils

import (
	"fmt"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
