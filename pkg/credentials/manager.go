package credentials

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/handlers/kafka"
	"github.com/nais/aivenator/pkg/handlers/opensearch"
	"github.com/nais/aivenator/pkg/handlers/secret"
	"github.com/nais/aivenator/pkg/handlers/valkey"
	"github.com/nais/aivenator/pkg/metrics"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
)

type Handler interface {
	Apply(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, secret *v1.Secret, logger log.FieldLogger) ([]v1.Secret, error)
	Cleanup(ctx context.Context, secret *v1.Secret, logger log.FieldLogger) error
}

type Manager struct {
	handlers []Handler
}

func NewManager(ctx context.Context, aiven *aiven.Client, kafkaProjects []string, mainProjectName string, logger log.FieldLogger) Manager {
	return Manager{
		handlers: []Handler{
			kafka.NewKafkaHandler(ctx, aiven, kafkaProjects, logger),
			opensearch.NewOpenSearchHandler(ctx, aiven, mainProjectName),
			secret.NewHandler(aiven, mainProjectName),
			valkey.NewValkeyHandler(ctx, aiven, mainProjectName),
		},
	}
}

func (c Manager) CreateSecret(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, sharedSecret *v1.Secret, logger log.FieldLogger) ([]v1.Secret, error) {
	var finalSecrets []v1.Secret
	for _, handler := range c.handlers {
		processingStart := time.Now()
		individualSecrets, err := handler.Apply(ctx, application, sharedSecret, logger)
		if err != nil {
			cleanupError := handler.Cleanup(ctx, sharedSecret, logger)
			if cleanupError != nil {
				return nil, fmt.Errorf("error during apply: %w, additionally, an error occured during cleanup: %v", err, cleanupError)
			}

			return nil, err
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

	return append(finalSecrets, *sharedSecret), nil
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
