package opensearch

import (
	"context"
	"fmt"
	"github.com/aiven/aiven-go-client"
	"github.com/nais/aivenator/pkg/aiven/project"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/utils"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
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
)

func NewOpenSearchHandler(ctx context.Context, aiven *aiven.Client, projectName string) OpenSearchHandler {
	return OpenSearchHandler{
		project:     project.NewManager(aiven.CA),
		serviceuser: serviceuser.NewManager(ctx, aiven.ServiceUsers),
		service:     service.NewManager(aiven.Services),
		projectName: projectName,
	}
}

type OpenSearchHandler struct {
	project     project.ProjectManager
	serviceuser serviceuser.ServiceUserManager
	service     service.ServiceManager
	projectName string
}

func (h OpenSearchHandler) Apply(application *aiven_nais_io_v1.AivenApplication, secret *v1.Secret, logger *log.Entry) error {
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

	addresses, err := h.service.GetServiceAddresses(h.projectName, serviceName)
	if err != nil {
		return utils.AivenFail("GetService", application, err, logger)
	}

	serviceUserName := fmt.Sprintf("%s%s", application.GetNamespace(), selectSuffix(spec.Access))

	aivenUser, err := h.serviceuser.Get(serviceUserName, h.projectName, serviceName, logger)
	if err != nil {
		return utils.AivenFail("GetServiceUser", application, err, logger)
	}

	secret.SetAnnotations(utils.MergeStringMap(secret.GetAnnotations(), map[string]string{
		ServiceUserAnnotation: aivenUser.Username,
		ProjectAnnotation:     h.projectName,
	}))
	logger.Infof("Fetched service user %s", aivenUser.Username)

	secret.StringData = utils.MergeStringMap(secret.StringData, map[string]string{
		OpenSearchUser:     aivenUser.Username,
		OpenSearchPassword: aivenUser.Password,
		OpenSearchURI:      addresses.OpenSearch,
	})

	return nil
}

func (h OpenSearchHandler) Cleanup(_ *v1.Secret, _ *log.Entry) error {
	return nil
}

func selectSuffix(access string) string {
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
