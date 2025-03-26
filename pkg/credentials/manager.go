package credentials

import (
	"context"
	"reflect"
	"time"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/handlers/influxdb"
	"github.com/nais/aivenator/pkg/handlers/kafka"
	"github.com/nais/aivenator/pkg/handlers/opensearch"
	"github.com/nais/aivenator/pkg/handlers/redis"
	"github.com/nais/aivenator/pkg/handlers/secret"
	"github.com/nais/aivenator/pkg/handlers/valkey"
	"github.com/nais/aivenator/pkg/metrics"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Handler interface {
	Apply(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, secret *corev1.Secret, logger log.FieldLogger) ([]*corev1.Secret, error)
	Cleanup(ctx context.Context, secret *corev1.Secret, logger *log.Entry) error
}

type Manager struct {
	handlers []Handler
}

func NewManager(ctx context.Context, k8s client.Client, aiven *aiven.Client, kafkaProjects []string, mainProjectName string, logger *log.Entry) Manager {
	return Manager{
		handlers: []Handler{
			influxdb.NewInfluxDBHandler(ctx, aiven, mainProjectName),
			kafka.NewKafkaHandler(ctx, aiven, kafkaProjects, logger),
			opensearch.NewOpenSearchHandler(ctx, k8s, aiven, mainProjectName),
			redis.NewRedisHandler(ctx, aiven, mainProjectName),
			secret.NewHandler(aiven, mainProjectName),
			valkey.NewValkeyHandler(ctx, valkey.K8s{Client: k8s}, aiven, mainProjectName),
		},
	}
}

func (c Manager) CreateSecret(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, secret *corev1.Secret, logger *log.Entry) ([]*corev1.Secret, error) {
	var secrets []*corev1.Secret
	for _, handler := range c.handlers {
		processingStart := time.Now()
		handlerSecrets, err := handler.Apply(ctx, application, secret, logger)
		if err != nil {
			return nil, err
		}

		used := time.Since(processingStart)
		handlerName := reflect.TypeOf(handler).String()
		metrics.HandlerProcessingTime.With(prometheus.Labels{
			metrics.LabelHandler: handlerName,
		}).Observe(used.Seconds())
		secrets = append(secrets, handlerSecrets...)
	}

	return secrets, nil
}

func (c Manager) Cleanup(ctx context.Context, s *corev1.Secret, logger *log.Entry) error {
	for _, handler := range c.handlers {
		err := handler.Cleanup(ctx, s, logger)
		if err != nil {
			return err
		}
	}

	return nil
}
