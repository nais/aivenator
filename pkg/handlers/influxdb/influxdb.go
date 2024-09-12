package influxdb

import (
	"context"
	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/utils"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"strconv"
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

func NewInfluxDBHandler(ctx context.Context, aiven *aiven.Client, projectName string) InfluxDBHandler {
	return InfluxDBHandler{
		service:     service.NewManager(aiven.Services),
		projectName: projectName,
	}
}

type InfluxDBHandler struct {
	service     service.ServiceManager
	projectName string
}

func (h InfluxDBHandler) Apply(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, secret *v1.Secret, logger log.FieldLogger) error {
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

	addresses, err := h.service.GetServiceAddresses(ctx, h.projectName, serviceName)
	if err != nil {
		return utils.AivenFail("GetService", application, err, true, logger)
	}

	aivenService, err := h.service.Get(ctx, h.projectName, serviceName)
	if err != nil {
		return utils.AivenFail("GetService", application, err, true, logger)
	}

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

	return nil
}

func (h InfluxDBHandler) Cleanup(ctx context.Context, secret *v1.Secret, logger *log.Entry) error {
	return nil
}
