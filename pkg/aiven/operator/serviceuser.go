package operator

import (
	"context"
	"fmt"

	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/utils"
	aiven_io_v1alpha1 "github.com/nais/liberator/pkg/apis/aiven.io/v1alpha1"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	ServiceUserAccessCert         = "SERVICEUSER_ACCESS_CERT"
	ServiceUserAccessKey          = "SERVICEUSER_ACCESS_KEY"
	ServiceUserCACert             = "SERVICEUSER_CA_CERT"
	ServiceUserHost               = "SERVICEUSER_HOST"
	ServiceUserPassword           = "SERVICEUSER_PASSWORD"
	ServiceUserPort               = "SERVICEUSER_PORT"
	ServiceUserSchemaRegistryHost = "SERVICEUSER_SCHEMA_REGISTRY_HOST"
	ServiceUserSchemaRegistryPort = "SERVICEUSER_SCHEMA_REGISTRY_PORT"
	ServiceUserUsername           = "SERVICEUSER_USERNAME"
)

type ServiceUser struct {
	Secret   map[string]string
	Username string
}

type ServiceUserManager interface {
	CreateServiceUser(ctx context.Context, owner client.Object, spec ServiceUserSpec, logger log.FieldLogger) (*ServiceUser, error)
	DeleteServiceUser(ctx context.Context, namespace, name string, logger log.FieldLogger) error
	Exists(ctx context.Context, namespace, name string) (bool, error)
	ServiceName(ctx context.Context, namespace, name string) (string, bool, error)
}

type ServiceUserSpec struct {
	AccessControl *aiven_io_v1alpha1.ServiceUserAccessControl
	Name          string
	Namespace     string
	Project       string
	ServiceName   string
	Username      string
}

type Manager struct {
	client client.Client
}

func NewServiceUserManager(client client.Client) ServiceUserManager {
	return &Manager{client: client}
}

func (m *Manager) CreateServiceUser(ctx context.Context, owner client.Object, spec ServiceUserSpec, logger log.FieldLogger) (*ServiceUser, error) {
	serviceUser := &aiven_io_v1alpha1.ServiceUser{
		ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: spec.Namespace},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, m.client, serviceUser, func() error {
		serviceUser.SetLabels(utils.MergeStringMap(serviceUser.GetLabels(), map[string]string{
			"app":  owner.GetName(),
			"team": owner.GetNamespace(),
		}))
		serviceUser.Spec.Project = spec.Project
		serviceUser.Spec.ServiceName = spec.ServiceName
		serviceUser.Spec.ConnInfoSecretTarget = aiven_io_v1alpha1.ConnInfoSecretTarget{Name: RawSecretName(spec.Name)}
		// spec.username is immutable (CRD CEL rule): set it only at creation and leave an
		// existing value untouched, so aivenator never issues a rejected update.
		// TODO: this emptiness-check assumes only Kafka ever sets a username - only needed until kafka can migrate service user names to same scheme as opensearch/valkey
		// Revisit before OpenSearch/Valkey do too, or an adopt could get silently stuck or rejected.
		if serviceUser.Spec.Username == "" {
			serviceUser.Spec.Username = spec.Username
		}
		if spec.AccessControl != nil {
			serviceUser.Spec.AccessControl = spec.AccessControl
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
			return nil, &utils.SecretNotReadyError{Namespace: spec.Namespace, Secret: secretName}
		}
		return nil, err
	}
	if app, ok := owner.(*aiven_nais_io_v1.AivenApplication); ok {
		utils.ClearSecretPending(app, secretName)
	}

	data := make(map[string]string, len(secret.Data))
	for key, value := range secret.Data {
		data[key] = string(value)
	}
	username, err := required(data, ServiceUserUsername)
	if err != nil {
		return nil, err
	}
	logger.WithField(utils.FieldInvariant, "ensured ServiceUser").Infof("ensured ServiceUser %s/%s", spec.Namespace, spec.Name)
	return &ServiceUser{Username: username, Secret: data}, nil
}

func (m *Manager) DeleteServiceUser(ctx context.Context, namespace, name string, logger log.FieldLogger) error {
	serviceUser := &aiven_io_v1alpha1.ServiceUser{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	if err := m.client.Delete(ctx, serviceUser); err != nil {
		return err
	}
	logger.WithField(utils.FieldInvariant, "deleted ServiceUser").Infof("deleted ServiceUser %s/%s", namespace, name)
	return nil
}

// Exists reports whether the ServiceUser CR is present, so Cleanup can pick CR
// deletion over the transitional direct-API path without a persisted marker.
func (m *Manager) Exists(ctx context.Context, namespace, name string) (bool, error) {
	serviceUser := &aiven_io_v1alpha1.ServiceUser{}
	if err := m.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, serviceUser); err != nil {
		if k8serrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (m *Manager) ServiceName(ctx context.Context, namespace, name string) (string, bool, error) {
	serviceUser := &aiven_io_v1alpha1.ServiceUser{}
	if err := m.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, serviceUser); err != nil {
		if k8serrors.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return serviceUser.Spec.ServiceName, true, nil
}

func ResolveExistingServiceUser(ctx context.Context, mgr ServiceUserManager, namespace, existingName, existingLegacy, serviceName string) (adopt, legacy string, err error) {
	legacy = existingLegacy
	switch {
	case existingName == "":
	case !utils.IsValidCRName(existingName):
		legacy = existingName
	default:
		sn, exists, lookupErr := mgr.ServiceName(ctx, namespace, existingName)
		switch {
		case lookupErr != nil:
			return "", legacy, fmt.Errorf("reading existing ServiceUser %s/%s: %w", namespace, existingName, lookupErr)
		case exists && sn != serviceName:
			return "", legacy, fmt.Errorf("ServiceUser %s/%s is bound to Aiven service %q, but the application now targets %q: %w", namespace, existingName, sn, serviceName, utils.ErrUnrecoverable)
		}
		adopt = existingName
	}
	return adopt, legacy, nil
}

func DrainServiceUser(ctx context.Context, crMgr ServiceUserManager, suMgr serviceuser.ServiceUserManager, namespace, crName, directTarget, projectName, serviceName string, logger log.FieldLogger) error {
	if crName != "" {
		exists, err := crMgr.Exists(ctx, namespace, crName)
		if err != nil {
			return err
		}
		if exists {
			if err := crMgr.DeleteServiceUser(ctx, namespace, crName, logger); err != nil {
				return err
			}
			logger.WithField(utils.FieldInvariant, "Deleted service user").Infof("Deleted service user %s", crName)
			return nil
		}
	}
	if directTarget != "" {
		return serviceuser.EnsureServiceUserDeleted(ctx, suMgr, "service user", directTarget, projectName, serviceName, logger)
	}
	return nil
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
