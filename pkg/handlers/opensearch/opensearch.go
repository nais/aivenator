package opensearch

import (
	"context"
	"fmt"
	"strconv"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/aiven/opensearch"
	"github.com/nais/aivenator/pkg/aiven/project"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/handlers/secret"
	"github.com/nais/aivenator/pkg/utils"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Annotations
const (
	ServiceUserAnnotation = "opensearch.aiven.nais.io/serviceUser"
	ServiceNameAnnotation = "opensearch.aiven.nais.io/serviceName"
	ProjectAnnotation     = "opensearch.aiven.nais.io/project"
)

// Environment variables
const (
	OpenSearchUser     = "OPEN_SEARCH_USERNAME"
	OpenSearchPassword = "OPEN_SEARCH_PASSWORD"
	OpenSearchURI      = "OPEN_SEARCH_URI"
	OpenSearchHost     = "OPEN_SEARCH_HOST"
	OpenSearchPort     = "OPEN_SEARCH_PORT"
)

type OpenSearchHandler struct {
	project        project.ProjectManager
	serviceuser    serviceuser.ServiceUserManager
	service        service.ServiceManager
	openSearchACL  opensearch.ACLManager
	projectName    string
	secretsHandler secret.Secrets
}

func NewOpenSearchHandler(ctx context.Context, k8s client.Client, aiven *aiven.Client, secretHandler *secret.Handler, projectName string) OpenSearchHandler {
	return OpenSearchHandler{
		project:        project.NewManager(aiven.CA),
		serviceuser:    serviceuser.NewManager(ctx, aiven.ServiceUsers),
		service:        service.NewManager(aiven.Services),
		openSearchACL:  aiven.OpenSearchACLs,
		projectName:    projectName,
		secretsHandler: secretHandler,
	}
}

func secretObjectKey(application *aiven_nais_io_v1.AivenApplication) client.ObjectKey {
	return client.ObjectKey{
		Namespace: application.GetNamespace(),
		Name:      application.Spec.OpenSearch.SecretName,
	}
}

func (h OpenSearchHandler) Apply(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, logger log.FieldLogger) ([]*corev1.Secret, error) {
	opensearch := application.Spec.OpenSearch
	if opensearch == nil {
		return nil, nil
	}

	var secret corev1.Secret
	usesNewStyleSecret := opensearch.SecretName != ""
	if usesNewStyleSecret {
		secret = h.secretsHandler.GetOrInitSecret(ctx, application.GetNamespace(), application.Spec.OpenSearch.SecretName, logger)
	} else {
		secret = h.secretsHandler.GetOrInitSecret(ctx, application.GetNamespace(), application.Spec.SecretName, logger)
	}
	err := h.secretsHandler.NormalizeSecret(ctx, application, &secret, logger)
	if err != nil {
		return nil, err
	}

	serviceName := opensearch.Instance

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

	aivenUser, err := h.provideServiceUser(ctx, application, serviceName, &secret, logger)
	if err != nil {
		return nil, err
	}

	secret.SetAnnotations(utils.MergeStringMap(secret.GetAnnotations(), map[string]string{
		ServiceUserAnnotation: aivenUser.Username,
		ServiceNameAnnotation: fmt.Sprintf("opensearch-%s-%s", application.GetNamespace(), serviceName),
		ProjectAnnotation:     h.projectName,
	}))

	logger.Infof("Fetched service user %s", aivenUser.Username)

	secret.StringData = utils.MergeStringMap(secret.StringData, map[string]string{
		OpenSearchUser:     aivenUser.Username,
		OpenSearchPassword: aivenUser.Password,
		OpenSearchURI:      addresses.OpenSearch.URI,
		OpenSearchHost:     addresses.OpenSearch.Host,
		OpenSearchPort:     strconv.Itoa(addresses.OpenSearch.Port),
	})

	return []*corev1.Secret{&secret}, nil
}

func (h OpenSearchHandler) provideServiceUser(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, serviceName string, secret *corev1.Secret, logger log.FieldLogger) (*aiven.ServiceUser, error) {
	var serviceUsername string
	if nameFromAnnotation, ok := secret.GetAnnotations()[ServiceUserAnnotation]; ok {
		serviceUsername = nameFromAnnotation
	} else {
		suffix, err := utils.CreateSuffix(application)
		if err != nil {
			err = fmt.Errorf("unable to create service user suffix: %s %w", err, utils.ErrUnrecoverable)
			utils.LocalFail("CreateSuffix", application, err, logger)
			return nil, err
		}

		serviceUsername = fmt.Sprintf("%s%s-%s", application.GetNamespace(), utils.SelectSuffix(application.Spec.OpenSearch.Access), suffix)
	}

	aivenUser, err := h.serviceuser.Get(ctx, serviceUsername, h.projectName, serviceName, logger)
	if err == nil {
		return aivenUser, nil
	}
	if !aiven.IsNotFound(err) {
		return nil, utils.AivenFail("GetServiceUser", application, err, false, logger)
	}

	aivenUser, err = h.serviceuser.Create(ctx, serviceUsername, h.projectName, serviceName, nil, logger)
	if err != nil {
		return nil, utils.AivenFail("CreateServiceUser", application, err, false, logger)
	}

	// TODO: Hvis dette feiler, så vil det ikke bli gjort noe forsøk på å fjerne service user.
	// As such, this should be one, atomic operation. As a compromise, we delete the serviceUser if updating the acl fails (the serviceuser is not usable without them)
	if err = h.updateACL(ctx, serviceUsername, application.Spec.OpenSearch.Access, h.projectName, serviceName); err != nil {
		errr := h.serviceuser.Delete(ctx, aivenUser.Username, h.projectName, serviceName, logger)
		if errr != nil {
			return nil, utils.AivenFail("DeleteServiceUser", application, err, false, logger)
		}
		return nil, utils.AivenFail("UpdateACL", application, err, false, logger)
	}

	return aivenUser, nil
}

func (h OpenSearchHandler) Cleanup(ctx context.Context, secret *corev1.Secret, logger log.FieldLogger) error {
	annotations := secret.GetAnnotations()

	if serviceName, okServiceName := annotations[ServiceNameAnnotation]; okServiceName {
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

func (h OpenSearchHandler) updateACL(ctx context.Context, serviceUserName string, access string, projectName string, serviceName string) error {
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
	if err != nil {
		return err
	}
	return nil
}
