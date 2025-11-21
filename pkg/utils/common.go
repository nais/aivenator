package utils

import (
	"encoding/base64"
	"fmt"
	"hash/crc32"
	"time"

	aiven_nais_io_v2 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v2"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func Expired(expiredAt time.Time) bool {
	return time.Now().After(expiredAt)
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

func CreateSuffix(application *aiven_nais_io_v2.AivenApplication) (string, error) {
	hasher := crc32.NewIEEE()
	basename := fmt.Sprintf("%d%s", application.Generation, application.Name)
	_, err := hasher.Write([]byte(basename))
	if err != nil {
		return "", err
	}
	bytes := make([]byte, 0, 4)
	suffix := base64.RawURLEncoding.EncodeToString(hasher.Sum(bytes))
	return suffix[:3], nil
}
