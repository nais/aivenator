package credentials

import (
	"context"
	"reflect"
	"time"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/metrics"
	"github.com/nais/aivenator/pkg/services/kafka"
	"github.com/nais/aivenator/pkg/services/opensearch"
	"github.com/nais/aivenator/pkg/services/valkey"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
)

type ServiceHandler interface {
	Apply(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, logger log.FieldLogger) ([]v1.Secret, error)
	Cleanup(ctx context.Context, secret *v1.Secret, logger log.FieldLogger) error
}

type Manager struct {
	handlers []ServiceHandler
}

func NewManager(ctx context.Context, aiven *aiven.Client, kafkaProjects []string, mainProjectName string, logger log.FieldLogger) Manager {
	return Manager{
		handlers: []ServiceHandler{
			kafka.NewKafkaHandler(ctx, aiven, kafkaProjects, mainProjectName, logger),
			opensearch.NewOpenSearchHandler(ctx, aiven, mainProjectName),
			valkey.NewValkeyHandler(ctx, aiven, mainProjectName),
		},
	}
}

func (c Manager) CreateSecret(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, logger log.FieldLogger) ([]v1.Secret, error) {
	var finalSecrets []v1.Secret
	logger.Info("Processing secrets.")
	for _, handler := range c.handlers {
		logger.WithField("ServiceHandler", reflect.TypeOf(handler).String())
		processingStart := time.Now()
		logger = logger.WithField("aivenService", reflect.TypeOf(handler).String())
		logger.Infof("Processing %s secrets.", reflect.TypeOf(handler).String())
		individualSecrets, err := handler.Apply(ctx, application, logger)
		if err != nil {
			return nil, err
		}
		for _, s := range individualSecrets {
			logger.Infof("Individual secret processed: %s", s.Name)
		}

		used := time.Since(processingStart)
		handlerName := reflect.TypeOf(handler).String()
		metrics.HandlerProcessingTime.With(prometheus.Labels{
			metrics.LabelHandler: handlerName,
		}).Observe(used.Seconds())

		if individualSecrets != nil {
			finalSecrets = append(finalSecrets, individualSecrets...)
		}
	}

	return finalSecrets, nil
}

func (c Manager) Cleanup(ctx context.Context, s *v1.Secret, logger log.FieldLogger) error {
	for _, handler := range c.handlers {
		err := handler.Cleanup(ctx, s, logger)
		if err != nil {
			return err
		}
	}

	return nil
}
