package opensearch

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/constants"
	operator "github.com/nais/aivenator/pkg/aiven/operator"
	"github.com/nais/aivenator/pkg/aiven/service"
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

// Annotations
const (
	ServiceUserAnnotation       = "opensearch.aiven.nais.io/serviceUser"
	ServiceNameAnnotation       = "opensearch.aiven.nais.io/serviceName"
	ProjectAnnotation           = "opensearch.aiven.nais.io/project"
	InstanceAnnotation          = "opensearch.aiven.nais.io/instance"
	DefaultACLAccess            = "read"
	LegacyServiceUserAnnotation = "opensearch.aiven.nais.io/legacyServiceUser"
)

// Environment variables
const (
	OpenSearchUser          = "OPEN_SEARCH_USERNAME"
	OpenSearchPassword      = "OPEN_SEARCH_PASSWORD"
	OpenSearchURI           = "OPEN_SEARCH_URI"
	OpenSearchHost          = "OPEN_SEARCH_HOST"
	OpenSearchPort          = "OPEN_SEARCH_PORT"
	OpenSearchDashboardURI  = "OPEN_SEARCH_DASHBOARD_URI"
	OpenSearchDashboardHost = "OPEN_SEARCH_DASHBOARD_HOST"
	OpenSearchDashboardPort = "OPEN_SEARCH_DASHBOARD_PORT"
)

func NewOpenSearchHandler(ctx context.Context, aiven *aiven.Client, projectName string, k8sReader client.Reader, crServiceUser operator.ServiceUserManager, openSearchACL operator.OpenSearchACLManager) OpenSearchHandler {
	return OpenSearchHandler{
		crServiceUser: crServiceUser,
		k8sReader:     k8sReader,
		openSearchACL: openSearchACL,
		projectName:   projectName,
		secretConfig:  utils.NewSecretConfig(aiven, projectName),
		service:       service.NewManager(aiven.Services),
		serviceuser:   serviceuser.NewManager(ctx, aiven.ServiceUsers),
	}
}

type OpenSearchHandler struct {
	crServiceUser operator.ServiceUserManager
	k8sReader     client.Reader
	openSearchACL operator.OpenSearchACLManager
	projectName   string
	secretConfig  utils.SecretConfig
	service       service.ServiceManager
	// serviceuser deletes transitional pre-CR users; drop once the drain is done.
	serviceuser serviceuser.ServiceUserManager
}

func (h OpenSearchHandler) Apply(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, logger log.FieldLogger) ([]corev1.Secret, error) {
	spec := application.Spec.OpenSearch
	if spec == nil {
		return nil, nil
	}

	namespace := application.GetNamespace()
	serviceName, instance, err := h.resolveInstance(ctx, namespace, spec.Instance)
	if err != nil {
		utils.LocalFail("ResolveOpenSearchInstance", application, err, logger)
		return nil, err
	}
	logger = logger.WithFields(log.Fields{
		"aivenProject":         h.projectName,
		"serviceName":          serviceName,
		"aivenServiceInstance": spec.Instance,
	})

	addresses, err := h.service.GetServiceAddresses(ctx, h.projectName, serviceName)
	if err != nil {
		return nil, utils.AivenFail("GetService", application, err, false, logger)
	}

	logger = logger.WithField("secretName", spec.SecretName)
	individualSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.SecretName,
			Namespace: namespace,
		},
	}

	serviceUser, legacyUsername, err := h.provideServiceUser(ctx, application, instance, serviceName, logger)
	if err != nil {
		return nil, err
	}
	logger = logger.WithField("serviceUser", serviceUser.Username)
	password, err := operator.Required(serviceUser.Secret, operator.ServiceUserPassword)
	if err != nil {
		return nil, utils.AivenFail("ReadServiceUserSecret", application, err, false, logger)
	}
	host, err := operator.Required(serviceUser.Secret, operator.ServiceUserHost)
	if err != nil {
		return nil, utils.AivenFail("ReadServiceUserSecret", application, err, false, logger)
	}
	port, err := operator.Required(serviceUser.Secret, operator.ServiceUserPort)
	if err != nil {
		return nil, utils.AivenFail("ReadServiceUserSecret", application, err, false, logger)
	}

	dashboard := addresses.OpenSearchDashboard()
	annotations := map[string]string{
		ServiceNameAnnotation: serviceName,
		ProjectAnnotation:     h.projectName,
		ServiceUserAnnotation: serviceUser.Username,
		InstanceAnnotation:    spec.Instance,
	}
	if legacyUsername != "" {
		annotations[LegacyServiceUserAnnotation] = legacyUsername
	}
	connection := map[string]string{
		OpenSearchUser:          serviceUser.Username,
		OpenSearchPassword:      password,
		OpenSearchURI:           fmt.Sprintf("https://%s:%s", host, port),
		OpenSearchHost:          host,
		OpenSearchPort:          port,
		OpenSearchDashboardURI:  dashboard.URI,
		OpenSearchDashboardHost: dashboard.Host,
		OpenSearchDashboardPort: strconv.Itoa(dashboard.Port),
	}

	// Initialise the secret's identity/labels/CA/timestamp last, once provisioning
	// has succeeded, so the project-CA fetch does not run on failure paths.
	if _, err := h.secretConfig.ApplyIndividualSecret(ctx, application, individualSecret, logger); err != nil {
		return nil, utils.AivenFail("GetOrInitSecret", application, err, false, logger)
	}

	individualSecret.SetAnnotations(utils.MergeStringMap(individualSecret.GetAnnotations(), annotations))
	individualSecret.StringData = utils.MergeStringMap(individualSecret.StringData, connection)

	controllerutil.AddFinalizer(individualSecret, constants.AivenatorFinalizer)

	logger.Infof("Applied individualSecret")
	return []corev1.Secret{*individualSecret}, nil
}

func (h OpenSearchHandler) resolveInstance(ctx context.Context, namespace, instance string) (string, client.Object, error) {
	newStyleName := fmt.Sprintf("opensearch-%s-%s", namespace, instance)
	cr, err := utils.GetResourceInNamespace(ctx, h.k8sReader, &aiven_io_v1alpha1.OpenSearch{}, newStyleName, namespace)
	if err == nil {
		return newStyleName, cr, nil
	}
	if !errors.Is(err, utils.ErrNotFound) {
		return "", nil, err
	}

	cr, err = utils.GetResourceInNamespace(ctx, h.k8sReader, &aiven_io_v1alpha1.OpenSearch{}, instance, namespace)
	if err != nil {
		return "", nil, err
	}
	return instance, cr, nil
}

func (h OpenSearchHandler) provideServiceUser(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, instance client.Object, serviceName string, logger log.FieldLogger) (*operator.ServiceUser, string, error) {
	if application.Spec.OpenSearch.Access == "" {
		application.Spec.OpenSearch.Access = DefaultACLAccess
	}
	namespace := application.GetNamespace()

	var existingName, existingLegacy string
	existing := &corev1.Secret{}
	if err := h.k8sReader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: application.Spec.OpenSearch.SecretName}, existing); err != nil {
		if !k8serrors.IsNotFound(err) {
			return nil, "", utils.AivenFail("GetSecret", application, err, false, logger)
		}
	} else {
		existingName = existing.GetAnnotations()[ServiceUserAnnotation]
		existingLegacy = existing.GetAnnotations()[LegacyServiceUserAnnotation]
	}
	res, err := operator.ResolveServiceUserName(ctx, h.crServiceUser, namespace, application.GetName(), application.Spec.OpenSearch.Access, application.Spec.OpenSearch.Instance, application.Spec.OpenSearch.SecretName, serviceName, existingName, existingLegacy, logger)
	if err != nil {
		return nil, "", utils.AivenFail("ResolveServiceUser", application, err, false, logger)
	}

	serviceUser, err := h.crServiceUser.CreateServiceUser(ctx, application, operator.ServiceUserSpec{
		Name:        res.Name,
		Namespace:   namespace,
		Project:     h.projectName,
		ServiceName: serviceName,
	}, logger)
	if err != nil {
		return nil, "", utils.AivenFail("EnsureServiceUser", application, err, false, logger)
	}

	if err := h.openSearchACL.CreateServiceUserACLs(ctx, instance, operator.OpenSearchACLSpec{
		Project:     h.projectName,
		ServiceName: serviceName,
		Namespace:   namespace,
		Username:    serviceUser.Username,
		Access:      application.Spec.OpenSearch.Access,
	}, logger); err != nil {
		return nil, "", utils.AivenFail("UpdateACL", application, err, false, logger)
	}
	return serviceUser, res.Legacy, nil
}

func (h OpenSearchHandler) Cleanup(ctx context.Context, secret *corev1.Secret, logger log.FieldLogger) error {
	annotations := secret.GetAnnotations()
	serviceName, okServiceName := annotations[ServiceNameAnnotation]
	if !okServiceName {
		return nil
	}

	serviceUserName, okServiceUser := annotations[ServiceUserAnnotation]
	if !okServiceUser {
		return fmt.Errorf("missing annotation %s", ServiceUserAnnotation)
	}
	projectName, okProjectName := annotations[ProjectAnnotation]
	if !okProjectName {
		return fmt.Errorf("missing annotation %s", ProjectAnnotation)
	}

	logger = logger.WithFields(log.Fields{
		"secretName":           secret.GetName(),
		"serviceUser":          serviceUserName,
		"aivenProject":         projectName,
		"serviceName":          serviceName,
		"aivenServiceInstance": annotations[InstanceAnnotation],
	})

	// The ACL entry is always removed via the OpenSearchACLConfig CR: once that
	// CR exists it is the single writer of the service's ACLs, and any direct
	// API removal would be reverted by aiven-operator.
	if err := h.openSearchACL.DeleteServiceUserACLs(ctx, secret.GetNamespace(), serviceName, serviceUserName, logger); err != nil {
		return err
	}

	if err := operator.DrainServiceUser(ctx, h.crServiceUser, h.serviceuser, secret.GetNamespace(), serviceUserName, serviceUserName, projectName, serviceName, logger); err != nil {
		return err
	}

	// A tracked legacy (pre-CR) user has no CR: its ACL entry is still removed
	// via the OpenSearchACLConfig CR (single writer), the user itself directly
	// via the Aiven API.
	if legacyUsername, ok := annotations[LegacyServiceUserAnnotation]; ok {
		if err := h.openSearchACL.DeleteServiceUserACLs(ctx, secret.GetNamespace(), serviceName, legacyUsername, logger); err != nil {
			return err
		}
		if err := serviceuser.EnsureServiceUserDeleted(ctx, h.serviceuser, "legacy service user", legacyUsername, projectName, serviceName, logger); err != nil {
			return err
		}
	}

	return nil
}
