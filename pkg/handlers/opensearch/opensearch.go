package opensearch

import (
	"context"
	"fmt"
	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/aiven/opensearch"
	"github.com/nais/aivenator/pkg/aiven/project"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/utils"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"strconv"
)

// Annotations
const (
	ServiceUserAnnotation = "opensearch.aiven.nais.io/serviceUser"
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

func NewOpenSearchHandler(ctx context.Context, aiven *aiven.Client, projectName string) OpenSearchHandler {
	return OpenSearchHandler{
		project:       project.NewManager(aiven.CA),
		serviceuser:   serviceuser.NewManager(ctx, aiven.ServiceUsers),
		service:       service.NewManager(aiven.Services),
		openSearchACL: aiven.OpenSearchACLs,
		projectName:   projectName,
	}
}

type OpenSearchHandler struct {
	project       project.ProjectManager
	serviceuser   serviceuser.ServiceUserManager
	service       service.ServiceManager
	openSearchACL opensearch.ACLManager
	projectName   string
}

func (h OpenSearchHandler) Apply(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, secret *v1.Secret, logger log.FieldLogger) error {
	logger = logger.WithFields(log.Fields{"handler": "opensearch"})
	spec := application.Spec.OpenSearch
	if spec == nil {
		return nil
	}

	serviceName := spec.Instance

	logger = logger.WithFields(log.Fields{
		"project": h.projectName,
		"service": serviceName,
	})

	addresses, err := h.service.GetServiceAddresses(ctx, h.projectName, serviceName)
	if err != nil {
		return utils.AivenFail("GetService", application, err, false, logger)
	}
	if len(addresses.OpenSearch.URI) == 0 {
		return utils.AivenFail("GetService", application, fmt.Errorf("no OpenSearch service found"), false, logger)
	}

	serviceUserName := fmt.Sprintf("%s%s", application.GetNamespace(), utils.SelectSuffix(spec.Access))

	aivenUser, err := h.serviceuser.Get(ctx, serviceUserName, h.projectName, serviceName, logger)
	if err != nil {
		if aiven.IsNotFound(err) {
			aivenUser, err = h.serviceuser.Create(ctx, serviceUserName, h.projectName, serviceName, nil, logger)
			if err != nil {
				return utils.AivenFail("CreateServiceUser", application, err, false, logger)
			}
			err = h.updateACL(ctx, serviceUserName, spec.Access, h.projectName, serviceName)
			if err != nil {
				return utils.AivenFail("UpdateACL", application, err, false, logger)
			}
		} else {
			return utils.AivenFail("GetServiceUser", application, err, false, logger)
		}
	}

	secret.SetAnnotations(utils.MergeStringMap(secret.GetAnnotations(), map[string]string{
		ServiceUserAnnotation: aivenUser.Username,
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

	return nil
}

func (h OpenSearchHandler) Cleanup(_ context.Context, _ *v1.Secret, _ *log.Entry) error {
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
