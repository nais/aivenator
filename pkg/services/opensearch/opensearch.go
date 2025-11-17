package opensearch

import (
	"context"
	"fmt"
	"strconv"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/aiven/opensearch"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/utils"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Annotations
const (
	ServiceUserAnnotation = "opensearch.aiven.nais.io/serviceUser"
	ServiceNameAnnotation = "opensearch.aiven.nais.io/serviceName"
	ProjectAnnotation     = "opensearch.aiven.nais.io/project"
	DefaultACLAccess      = "read"
)

// Environment variables
const (
	OpenSearchUser     = "OPEN_SEARCH_USERNAME"
	OpenSearchPassword = "OPEN_SEARCH_PASSWORD"
	OpenSearchURI      = "OPEN_SEARCH_URI"
	OpenSearchHost     = "OPEN_SEARCH_HOST"
	OpenSearchPort     = "OPEN_SEARCH_PORT"
)

func NewOpenSearchHandler(ctx context.Context, aiven *aiven.Client, projectName string) OpenSearchHandler {
	return OpenSearchHandler{
		serviceuser:   serviceuser.NewManager(ctx, aiven.ServiceUsers),
		service:       service.NewManager(aiven.Services),
		openSearchACL: aiven.OpenSearchACLs,
		secretConfig:  utils.NewSecretConfig(aiven, projectName),
		projectName:   projectName,
	}
}

type OpenSearchHandler struct {
	serviceuser   serviceuser.ServiceUserManager
	service       service.ServiceManager
	openSearchACL opensearch.ACLManager
	secretConfig  utils.SecretConfig
	projectName   string
}

func (h OpenSearchHandler) Apply(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, logger log.FieldLogger) ([]corev1.Secret, error) {
	spec := application.Spec.OpenSearch
	if spec == nil {
		return nil, nil
	}

	serviceName := spec.Instance

	logger = logger.WithFields(log.Fields{
		"handler": "opensearch",
		"project": h.projectName,
		"service": serviceName,
	})

	addresses, err := h.service.GetServiceAddresses(ctx, h.projectName, serviceName)
	if err != nil {
		return nil, utils.AivenFail("GetService", application, err, false, logger)
	}
	if len(addresses.OpenSearch.URI) == 0 {
		return nil, utils.AivenFail("GetService", application, fmt.Errorf("no OpenSearch service found"), false, logger)
	}

	logger = logger.WithField("individualSecret", spec.SecretName)
	individualSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.SecretName,
			Namespace: application.GetNamespace(),
		},
	}

	_, err = h.secretConfig.ApplyIndividualSecret(ctx, application, individualSecret, logger)
	if err != nil {
		return nil, utils.AivenFail("GetOrInitSecret", application, err, false, logger)
	}
	aivenUser, err := h.provideServiceUser(ctx, application, serviceName, individualSecret, logger)
	if err != nil {
		return nil, err
	}

	individualSecret.SetAnnotations(utils.MergeStringMap(individualSecret.GetAnnotations(), map[string]string{
		ServiceUserAnnotation: aivenUser.Username,
		ServiceNameAnnotation: serviceName,
		ProjectAnnotation:     h.projectName,
	}))

	individualSecret.StringData = utils.MergeStringMap(individualSecret.StringData, map[string]string{
		OpenSearchUser:     aivenUser.Username,
		OpenSearchPassword: aivenUser.Password,
		OpenSearchURI:      addresses.OpenSearch.URI,
		OpenSearchHost:     addresses.OpenSearch.Host,
		OpenSearchPort:     strconv.Itoa(addresses.OpenSearch.Port),
	})

	controllerutil.AddFinalizer(individualSecret, constants.AivenatorFinalizer)

	logger.Infof("Applied individualSecret")
	return []corev1.Secret{*individualSecret}, nil
}

func (h OpenSearchHandler) provideServiceUser(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, serviceName string, secret *corev1.Secret, logger log.FieldLogger) (*aiven.ServiceUser, error) {
	var serviceUserName string

	if application.Spec.OpenSearch.Access == "" {
		application.Spec.OpenSearch.Access = DefaultACLAccess
	}

	if nameFromAnnotation, ok := secret.GetAnnotations()[ServiceUserAnnotation]; ok {
		serviceUserName = nameFromAnnotation
	} else {
		suffix, err := utils.CreateSuffix(application)
		if err != nil {
			err = fmt.Errorf("unable to create service user suffix: %s %w", err, utils.ErrUnrecoverable)
			utils.LocalFail("CreateSuffix", application, err, logger)
			return nil, err
		}

		serviceUserName = fmt.Sprintf("%s%s-%s", application.GetNamespace(), utils.SelectSuffix(application.Spec.OpenSearch.Access), suffix)
	}

	logger.Info("attempting to get existing user")
	aivenUser, err := h.serviceuser.Get(ctx, serviceUserName, h.projectName, serviceName, logger)
	if err != nil && !aiven.IsNotFound(err) {
		return nil, utils.AivenFail("GetServiceUser", application, err, false, logger)
	}

	if aiven.IsNotFound(err) {
		logger.Info("user does not exist, creating")
		aivenUser, err = h.serviceuser.Create(ctx, serviceUserName, h.projectName, serviceName, nil, logger)
		if err != nil {
			return nil, utils.AivenFail("CreateServiceUser", application, err, false, logger)
		}
	}

	logger.Infof("updating ACLs for %s", aivenUser.Username)
	if err := h.updateACL(ctx, aivenUser.Username, application.Spec.OpenSearch.Access, h.projectName, serviceName); err != nil {
		return nil, utils.AivenFail("UpdateACL", application, err, false, logger)
	}

	logger.Infof("provided serviceuser: %v", aivenUser.Username)
	return aivenUser, nil
}

func (h OpenSearchHandler) updateACL(ctx context.Context, serviceUserName, access, projectName, serviceName string) error {
	resp, err := h.openSearchACL.Get(ctx, projectName, serviceName)
	if err != nil {
		return err
	}

	config := resp.OpenSearchACLConfig
	config.Enabled = true
	config.Add(aiven.OpenSearchACL{
		Rules: []aiven.OpenSearchACLRule{
			{Index: "_*", Permission: access},
			{Index: "*", Permission: access},
		},
		Username: serviceUserName,
	})
	_, err = h.openSearchACL.Update(ctx, projectName, serviceName, aiven.OpenSearchACLRequest{
		OpenSearchACLConfig: config,
	})

	return err
}

func (h OpenSearchHandler) Cleanup(ctx context.Context, secret *corev1.Secret, logger log.FieldLogger) error {
	annotations := secret.GetAnnotations()

	if serviceName, okServiceName := annotations[ServiceNameAnnotation]; okServiceName {
		logger = logger.WithField("secret_name", secret.GetName())

		serviceUser, okServiceUser := annotations[ServiceUserAnnotation]
		if !okServiceUser {
			return fmt.Errorf("missing annotation %s", ServiceUserAnnotation)
		}

		projectName, okProjectName := annotations[ProjectAnnotation]
		if !okProjectName {
			return fmt.Errorf("missing annotation %s", ProjectAnnotation)
		}

		logger = logger.WithFields(log.Fields{
			"serviceUser": serviceUser,
			"project":     projectName,
		})

		aclResp, err := h.openSearchACL.Get(ctx, projectName, serviceName)
		if err != nil {
			return err
		}

		aclConfig := aclResp.OpenSearchACLConfig
		newACLConfig := aclConfig.Delete(ctx, aiven.OpenSearchACL{Username: serviceUser})
		_, err = h.openSearchACL.Update(ctx, projectName, serviceName, aiven.OpenSearchACLRequest{
			OpenSearchACLConfig: *newACLConfig,
		})
		if err != nil {
			return err
		}

		if err := h.serviceuser.Delete(ctx, serviceUser, projectName, serviceName, logger); err != nil {
			if aiven.IsNotFound(err) {
				logger.Infof("Service user %s does not exist", serviceUser)
				return nil
			}

			return err
		}

		logger.Infof("Deleted service user %s", serviceUser)
	}

	return nil
}
