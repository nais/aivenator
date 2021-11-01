package elastic

import (
	"fmt"

	"github.com/aiven/aiven-go-client"
	"github.com/nais/aivenator/pkg/aiven/project"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/utils"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
)

// Annotations
const (
	ServiceUserAnnotation = "elastic.aiven.nais.io/serviceUser"
	ProjectAnnotation     = "elastic.aiven.nais.io/project"
)

// Environment variables
const (
	ElasticUser     = "ELASTIC_USERNAME"
	ElasticPassword = "ELASTIC_PASSWORD"
	ElasticURI      = "ELASTIC_URI"
)

func NewElasticHandler(aiven *aiven.Client, projectName string) ElasticHandler {
	return ElasticHandler{
		project:     project.NewManager(aiven.CA),
		serviceuser: serviceuser.NewManager(aiven.ServiceUsers),
		service:     service.NewManager(aiven.Services),
		projectName: projectName,
	}
}

type ElasticHandler struct {
	project     project.ProjectManager
	serviceuser serviceuser.ServiceUserManager
	service     service.ServiceManager
	projectName string
}

func (h ElasticHandler) Apply(application *aiven_nais_io_v1.AivenApplication, _ *appsv1.ReplicaSet, secret *v1.Secret, logger *log.Entry) error {
	logger = logger.WithFields(log.Fields{"handler": "elastic"})
	spec := application.Spec.Elastic
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
	logger.Infof("Fetched serviceuser %s", aivenUser.Username)

	secret.StringData = utils.MergeStringMap(secret.StringData, map[string]string{
		ElasticUser:     aivenUser.Username,
		ElasticPassword: aivenUser.Password,
		ElasticURI:      addresses.ElasticSearch,
	})

	return nil
}

func (h ElasticHandler) Cleanup(_ *v1.Secret, _ *log.Entry) error {
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
