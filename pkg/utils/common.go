package utils

import (
	"encoding/base64"
	"fmt"
	"hash/crc32"
	"os"
	"time"

	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func SelectSuffix(access string) string {
	switch access {
	case "admin":
		return ""
	case "write":
		return "-w"
	case "readwrite":
		return "-rw"
	default:
		return "-r"
	}
}

func CreateSuffix(application *aiven_nais_io_v1.AivenApplication) (string, error) {
	hasher := crc32.NewIEEE()
	basename := fmt.Sprintf("%d%s", application.Generation, os.Getenv("NAIS_CLUSTER_NAME"))
	_, err := hasher.Write([]byte(basename))
	if err != nil {
		return "", err
	}
	bytes := make([]byte, 0, 4)
	suffix := base64.RawURLEncoding.EncodeToString(hasher.Sum(bytes))
	return suffix[:3], nil
}
