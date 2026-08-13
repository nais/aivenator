package utils

import (
	"encoding/base64"
	"fmt"
	"hash/crc32"
	"strings"
	"time"

	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
)

// accessCode maps an AivenApplication access level to its one/two-letter name
// component. Unknown or empty access is treated as read, matching the handlers'
// default.
func accessCode(access string) string {
	switch access {
	case "admin":
		return "a"
	case "readwrite":
		return "rw"
	case "write":
		return "w"
	default:
		return "r"
	}
}

// IsValidCRName reports whether name can be used as a Kubernetes object name
// (lowercase RFC 1123 subdomain). Usernames minted by the pre-CR aivenator
// (CreateSuffix) used a base64 suffix alphabet (uppercase, '_') and fail this,
// so they cannot be adopted as ServiceUser CRs.
func IsValidCRName(name string) bool {
	return len(validation.IsDNS1123Subdomain(name)) == 0
}

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

// ServiceUserName builds the ServiceUser CR name — which IS the Aiven username
// until aiven-operator#1238 ships spec.username — for the team-scoped services
// (opensearch, valkey):
//
//	<app>-<access>-<h1>-<h2>-<YYYYwWW>
//	access = a | rw | w | r
//	h1     = crc32(<app>-<access>-<instance>), first 6 hex — the stable family
//	         key; excludes secretName and week, so <app>-<access>-<h1> is one
//	         logical user across all its rotations.
//	h2     = crc32(<secretName>), first 5 hex — carries the secret's identity so
//	         two coexisting secrets get distinct users and Cleanup can never
//	         delete a user another live secret still depends on.
//	YYYYwWW = ISO week-year and week at mint time; ensures users are unique per week.
//
// Only the app component is ever truncated, deterministically, to keep the whole
// name within Aiven's 64-char limit. It deliberately does not use liberator's
// ServiceUserNameWithSuffix (kafka's scheme): that joins with '_', invalid in a
// Kubernetes object name, and its team+app scope reflects kafka's single shared
// service, while these users are unique per team-scoped service.
func ServiceUserName(appName, access, instanceName, secretName string, mintTime time.Time) string {
	return ServiceUserNamePrefix(appName, access, instanceName, secretName) + WeekTail(mintTime)
}

// WeekTail is the "-YYYYwWW" suffix distinguishing a family's weekly mints.
func WeekTail(mintTime time.Time) string {
	year, week := mintTime.ISOWeek()
	return fmt.Sprintf("-%04dw%02d", year, week)
}

// weekTailLen is the byte length of the "-YYYYwWW" tail ServiceUserName appends.
const weekTailLen = len("-2006w01")

// ServiceUserNamePrefix is ServiceUserName without the week tail: the stable key
// of one logical user across its weekly rotations, so a retry can recover a name
// minted in an earlier week instead of stranding that CR (operator.FindAdoptable).
func ServiceUserNamePrefix(appName, access, instanceName, secretName string) string {
	code := accessCode(access)
	h1 := fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(appName+"-"+code+"-"+instanceName)))[:6]
	h2 := fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(secretName)))[:5]
	tail := fmt.Sprintf("-%s-%s-%s", code, h1, h2)

	if max := aiven_nais_io_v1.MaxServiceUserNameLength - len(tail) - weekTailLen; len(appName) > max {
		appName = strings.TrimRight(appName[:max], "-")
	}
	return appName + tail
}

func CreateSuffix(application *aiven_nais_io_v1.AivenApplication) (string, error) {
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
