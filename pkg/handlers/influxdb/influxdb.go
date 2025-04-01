package influxdb

import (
	"context"
	"strconv"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/handlers/secret"
	"github.com/nais/aivenator/pkg/utils"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
)

const (
	ServiceUserAnnotation = "influxdb.aiven.nais.io/serviceUser"
	ProjectAnnotation     = "influxdb.aiven.nais.io/project"
)

// Environment variables
const (
	InfluxDBUser     = "INFLUXDB_USERNAME"
	InfluxDBPassword = "INFLUXDB_PASSWORD"
	InfluxDBURI      = "INFLUXDB_URI"
	InfluxDBHost     = "INFLUXDB_HOST"
	InfluxDBPort     = "INFLUXDB_PORT"
	InfluxDBName     = "INFLUXDB_NAME"
)

func NewInfluxDBHandler(ctx context.Context, aiven *aiven.Client, secretHandler *secret.Handler, projectName string) InfluxDBHandler {
	return InfluxDBHandler{
		service:        service.NewManager(aiven.Services),
		projectName:    projectName,
		secretsHandler: secretHandler,
	}
}

type InfluxDBHandler struct {
	service        service.ServiceManager
	projectName    string
	secretsHandler secret.Secrets
}

func (h InfluxDBHandler) Apply(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, logger log.FieldLogger) ([]*v1.Secret, error) {
	logger = logger.WithFields(log.Fields{"handler": "influxdb"})
	if application.Spec.InfluxDB == nil {
		return nil, nil
	}

	spec := application.Spec.InfluxDB
	serviceName := spec.Instance

	logger = logger.WithFields(log.Fields{
		"project": h.projectName,
		"service": serviceName,
	})

	addresses, err := h.service.GetServiceAddresses(ctx, h.projectName, serviceName)
	if err != nil {
		return nil, utils.AivenFail("GetService", application, err, true, logger)
	}

	aivenService, err := h.service.Get(ctx, h.projectName, serviceName)
	if err != nil {
		return nil, utils.AivenFail("GetService", application, err, true, logger)
	}

	secret := h.secretsHandler.GetOrInitSecret(ctx, application.GetNamespace(), application.Spec.SecretName, logger)
	secret.SetAnnotations(utils.MergeStringMap(secret.GetAnnotations(), map[string]string{
		ServiceUserAnnotation: aivenService.ConnectionInfo.InfluxDBUsername,
		ProjectAnnotation:     h.projectName,
	}))

	secret.StringData = utils.MergeStringMap(secret.StringData, map[string]string{
		InfluxDBUser:     aivenService.ConnectionInfo.InfluxDBUsername,
		InfluxDBPassword: aivenService.ConnectionInfo.InfluxDBPassword,
		InfluxDBURI:      addresses.InfluxDB.URI,
		InfluxDBHost:     addresses.InfluxDB.Host,
		InfluxDBPort:     strconv.Itoa(addresses.InfluxDB.Port),
		InfluxDBName:     aivenService.ConnectionInfo.InfluxDBDatabaseName,
	})

	return []*v1.Secret{&secret}, nil
}

func (h InfluxDBHandler) Cleanup(ctx context.Context, secret *v1.Secret, logger log.FieldLogger) error {
	return nil
}
