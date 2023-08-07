package influxdb

import (
	"context"
	"github.com/aiven/aiven-go-client"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/utils"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
)

// Annotations
const (
	ServiceUserAnnotation = "influxdb.aiven.nais.io/serviceUser"
	ProjectAnnotation     = "influxdb.aiven.nais.io/project"
	ServiceUserName       = "avnadmin"
)

// Environment variables
const (
	InfluxDBUser     = "INFLUXDB_USERNAME"
	InfluxDBPassword = "INFLUXDB_PASSWORD"
	InfluxDBURI      = "INFLUXDB_URI"
)

func NewInfluxDBHandler(ctx context.Context, aiven *aiven.Client, projectName string) InfluxDBHandler {
	return InfluxDBHandler{
		serviceuser: serviceuser.NewManager(ctx, aiven.ServiceUsers),
		service:     service.NewManager(aiven.Services),
		projectName: projectName,
	}
}

type InfluxDBHandler struct {
	serviceuser serviceuser.ServiceUserManager
	service     service.ServiceManager
	projectName string
}

func (h InfluxDBHandler) Apply(application *aiven_nais_io_v1.AivenApplication, secret *v1.Secret, logger log.FieldLogger) error {
	logger = logger.WithFields(log.Fields{"handler": "influxdb"})
	if application.Spec.InfluxDB == nil {
		return nil
	}

	spec := application.Spec.InfluxDB
	serviceName := spec.Instance

	logger = logger.WithFields(log.Fields{
		"project": h.projectName,
		"service": serviceName,
	})

	addresses, err := h.service.GetServiceAddresses(h.projectName, serviceName)
	if err != nil {
		return utils.AivenFail("GetService", application, err, true, logger)
	}

	aivenUser, err := h.serviceuser.Get(ServiceUserName, h.projectName, serviceName, logger)
	if err != nil {
		if aiven.IsNotFound(err) {
			aivenUser, err = h.serviceuser.Create(ServiceUserName, h.projectName, serviceName, nil, logger)
			if err != nil {
				return utils.AivenFail("CreateServiceUser", application, err, false, logger)
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
		InfluxDBUser:     aivenUser.Username,
		InfluxDBPassword: aivenUser.Password,
		InfluxDBURI:      addresses.InfluxDB,
	})

	return nil
}

func (h InfluxDBHandler) Cleanup(_ *v1.Secret, _ *log.Entry) error {
	return nil
}
