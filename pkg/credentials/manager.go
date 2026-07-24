package credentials

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/aiven/aiven-go-client/v2"
	operator "github.com/nais/aivenator/pkg/aiven/operator"
	"github.com/nais/aivenator/pkg/metrics"
	"github.com/nais/aivenator/pkg/services/kafka"
	"github.com/nais/aivenator/pkg/services/opensearch"
	"github.com/nais/aivenator/pkg/services/valkey"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ServiceHandler interface {
	Apply(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, logger log.FieldLogger) ([]v1.Secret, error)
	Cleanup(ctx context.Context, secret *v1.Secret, logger log.FieldLogger) error
}

type Manager struct {
	handlers []ServiceHandler
}

func NewManager(ctx context.Context, aiven *aiven.Client, kafkaProjects []string, mainProjectName string, logger log.FieldLogger, k8sReader client.Reader, k8sClient client.Client) Manager {
	crServiceUser := operator.NewServiceUserManager(k8sClient)
	openSearchACL := operator.NewOpenSearchACLManager(k8sClient, aiven.OpenSearchACLs)
	return Manager{
		handlers: []ServiceHandler{
			kafka.NewKafkaHandler(ctx, aiven, kafkaProjects, mainProjectName, logger),
			opensearch.NewOpenSearchHandler(ctx, aiven, mainProjectName, k8sReader, crServiceUser, openSearchACL),
			valkey.NewValkeyHandler(ctx, aiven, mainProjectName, k8sReader, crServiceUser),
		},
	}
}

func (c Manager) CreateSecrets(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, logger log.FieldLogger) ([]v1.Secret, error) {
	var finalSecrets []v1.Secret
	var errs []error
	for _, handler := range c.handlers {
		handlerName := reflect.TypeOf(handler).String()
		handlerLogger := logger.WithField("handler", handlerName)
		handlerLogger.Infof("Processing %s secrets.", handlerName)

		processingStart := time.Now()
		individualSecrets, err := handler.Apply(ctx, application, handlerLogger)
		used := time.Since(processingStart)

		metrics.HandlerProcessingTime.With(prometheus.Labels{
			metrics.LabelHandler: handlerName,
		}).Observe(used.Seconds())

		finalSecrets = append(finalSecrets, individualSecrets...)

		if err != nil {
			handlerLogger.Errorf("%s failed: %s", handlerName, err)
			errs = append(errs, fmt.Errorf("%s: %w", handlerName, err))
			continue
		}

		for _, s := range individualSecrets {
			handlerLogger.Infof("Individual secret processed: %s", s.Name)
		}
	}

	return finalSecrets, errors.Join(errs...)
}

func (c Manager) Cleanup(ctx context.Context, s *v1.Secret, logger log.FieldLogger) error {
	for _, handler := range c.handlers {
		handlerLogger := logger.WithField("handler", reflect.TypeOf(handler).String())
		err := handler.Cleanup(ctx, s, handlerLogger)
		if err != nil {
			return err
		}
	}

	return nil
}
