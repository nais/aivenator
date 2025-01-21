package credentials

import (
	"context"
	"fmt"
	"github.com/nais/aivenator/pkg/handlers/influxdb"
	"github.com/nais/aivenator/pkg/handlers/redis"
	"github.com/nais/aivenator/pkg/handlers/valkey"
	"github.com/nais/aivenator/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"reflect"
	"time"

	aivenv1 "github.com/aiven/aiven-go-client"
	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/handlers/kafka"
	"github.com/nais/aivenator/pkg/handlers/opensearch"
	"github.com/nais/aivenator/pkg/handlers/secret"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
)

type Handler interface {
	Apply(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, secret *v1.Secret, logger log.FieldLogger) error
	Cleanup(ctx context.Context, secret *v1.Secret, logger *log.Entry) error
}

type Manager struct {
	handlers []Handler
}

func NewManager(ctx context.Context, aiven *aiven.Client, kafkaProjects []string, mainProjectName string, logger *log.Entry, aivenv1 *aivenv1.Client) Manager {
	return Manager{
		handlers: []Handler{
			secret.NewHandler(aiven, mainProjectName),
			kafka.NewKafkaHandler(ctx, aiven, kafkaProjects, logger, aivenv1),
			opensearch.NewOpenSearchHandler(ctx, aiven, mainProjectName),
			redis.NewRedisHandler(ctx, aiven, mainProjectName),
			valkey.NewValkeyHandler(ctx, aiven, mainProjectName),
			influxdb.NewInfluxDBHandler(ctx, aiven, mainProjectName),
		},
	}
}

func (c Manager) CreateSecret(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, secret *v1.Secret, logger *log.Entry) (*v1.Secret, error) {
	for _, handler := range c.handlers {
		processingStart := time.Now()
		err := handler.Apply(ctx, application, secret, logger)
		if err != nil {
			cleanupError := c.Cleanup(ctx, secret, logger)
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
	}
	return secret, nil
}

func (c Manager) Cleanup(ctx context.Context, s *v1.Secret, logger *log.Entry) error {
	for _, handler := range c.handlers {
		err := handler.Cleanup(ctx, s, logger)
		if err != nil {
			return err
		}
	}
	return nil
}
