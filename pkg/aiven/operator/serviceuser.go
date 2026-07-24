package operator

import (
	"context"
	"fmt"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/utils"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	ServiceUserUsername           = "SERVICEUSER_USERNAME"
	ServiceUserPassword           = "SERVICEUSER_PASSWORD"
	ServiceUserHost               = "SERVICEUSER_HOST"
	ServiceUserPort               = "SERVICEUSER_PORT"
	ServiceUserAccessCert         = "SERVICEUSER_ACCESS_CERT"
	ServiceUserAccessKey          = "SERVICEUSER_ACCESS_KEY"
	ServiceUserCACert             = "SERVICEUSER_CA_CERT"
	ServiceUserSchemaRegistryHost = "SERVICEUSER_SCHEMA_REGISTRY_HOST"
	ServiceUserSchemaRegistryPort = "SERVICEUSER_SCHEMA_REGISTRY_PORT"
)

var serviceUserGVK = schema.GroupVersionKind{Group: "aiven.io", Version: "v1alpha1", Kind: "ServiceUser"}

type ServiceUser struct {
	Username string
	Secret   map[string]string
}

type ServiceUserManager interface {
	CreateServiceUser(ctx context.Context, owner client.Object, spec ServiceUserSpec, logger log.FieldLogger) (*ServiceUser, error)
	DeleteServiceUser(ctx context.Context, namespace, name string, logger log.FieldLogger) error
	Exists(ctx context.Context, namespace, name string) (bool, error)
	ServiceName(ctx context.Context, namespace, name string) (string, bool, error)
}

type ServiceUserSpec struct {
	Name          string
	Namespace     string
	Project       string
	ServiceName   string
	AccessControl map[string]any
}

type Manager struct {
	client client.Client
}

func NewServiceUserManager(client client.Client) ServiceUserManager {
	return &Manager{client: client}
}

func (m *Manager) CreateServiceUser(ctx context.Context, owner client.Object, spec ServiceUserSpec, logger log.FieldLogger) (*ServiceUser, error) {
	serviceUser := &unstructured.Unstructured{}
	serviceUser.SetGroupVersionKind(serviceUserGVK)
	serviceUser.SetName(spec.Name)
	serviceUser.SetNamespace(spec.Namespace)

	_, err := controllerutil.CreateOrUpdate(ctx, m.client, serviceUser, func() error {
		serviceUser.SetGroupVersionKind(serviceUserGVK)
		serviceUser.SetLabels(utils.MergeStringMap(serviceUser.GetLabels(), map[string]string{
			"app":  owner.GetName(),
			"team": owner.GetNamespace(),
		}))
		specObject := map[string]any{
			"project":     spec.Project,
			"serviceName": spec.ServiceName,
			"connInfoSecretTarget": map[string]any{
				"name": RawSecretName(spec.Name),
			},
		}
		if spec.AccessControl != nil {
			specObject["accessControl"] = spec.AccessControl
		}
		if err := unstructured.SetNestedMap(serviceUser.Object, specObject, "spec"); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	secret := &corev1.Secret{}
	secretName := RawSecretName(spec.Name)
	if err := m.client.Get(ctx, client.ObjectKey{Namespace: spec.Namespace, Name: secretName}, secret); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, fmt.Errorf("serviceuser secret %s/%s not found: %w", spec.Namespace, secretName, utils.ErrNotFound)
		}
		return nil, err
	}

	data := make(map[string]string, len(secret.Data))
	for key, value := range secret.Data {
		data[key] = string(value)
	}
	username, err := required(data, ServiceUserUsername)
	if err != nil {
		return nil, err
	}
	logger.Infof("ensured ServiceUser %s/%s", spec.Namespace, spec.Name)
	return &ServiceUser{Username: username, Secret: data}, nil
}

func (m *Manager) DeleteServiceUser(ctx context.Context, namespace, name string, logger log.FieldLogger) error {
	serviceUser := &unstructured.Unstructured{}
	serviceUser.SetGroupVersionKind(serviceUserGVK)
	serviceUser.SetName(name)
	serviceUser.SetNamespace(namespace)
	err := m.client.Delete(ctx, serviceUser)
	if k8serrors.IsNotFound(err) {
		return aiven.Error{Message: fmt.Sprintf("ServiceUser %s/%s not found", namespace, name), Status: 404}
	}
	if err == nil {
		logger.Infof("deleted ServiceUser %s/%s", namespace, name)
	}
	return err
}

// Exists reports whether the ServiceUser CR is present, so Cleanup can pick CR
// deletion over the transitional direct-API path without a persisted marker.
func (m *Manager) Exists(ctx context.Context, namespace, name string) (bool, error) {
	serviceUser := &unstructured.Unstructured{}
	serviceUser.SetGroupVersionKind(serviceUserGVK)
	if err := m.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, serviceUser); err != nil {
		if k8serrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (m *Manager) ServiceName(ctx context.Context, namespace, name string) (string, bool, error) {
	serviceUser := &unstructured.Unstructured{}
	serviceUser.SetGroupVersionKind(serviceUserGVK)
	if err := m.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, serviceUser); err != nil {
		if k8serrors.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, err
	}
	serviceName, _, err := unstructured.NestedString(serviceUser.Object, "spec", "serviceName")
	return serviceName, true, err
}

// ResolveExistingServiceUser reuses the username annotated on an app-facing secret only when its CR is absent or still targets serviceName (spec.serviceName is immutable.
// Thus, a failed read or a mismatch mints fresh instead of re-pointing it); otherwise adopt is "" and the caller mints.
// legacy is a pre-CR username to drain via the direct Aiven API.
func ResolveExistingServiceUser(ctx context.Context, mgr ServiceUserManager, namespace, existingName, existingLegacy, serviceName string) (adopt, legacy string) {
	legacy = existingLegacy
	switch {
	case existingName == "":
	case !utils.IsValidCRName(existingName):
		legacy = existingName
	default:
		if sn, exists, err := mgr.ServiceName(ctx, namespace, existingName); err == nil && (!exists || sn == serviceName) {
			adopt = existingName
		}
	}
	return adopt, legacy
}

func Required(data map[string]string, key string) (string, error) {
	return required(data, key)
}

func RawSecretName(serviceUserName string) string {
	return "aivenator-raw-" + serviceUserName
}

func required(data map[string]string, key string) (string, error) {
	value, ok := data[key]
	if !ok || value == "" {
		return "", fmt.Errorf("missing %s in ServiceUser secret: %w", key, utils.ErrNotFound)
	}
	return value, nil
}
