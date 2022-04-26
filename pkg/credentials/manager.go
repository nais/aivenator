package credentials

import (
	"context"
	"fmt"

	"github.com/aiven/aiven-go-client"
	"github.com/nais/aivenator/pkg/handlers/kafka"
	"github.com/nais/aivenator/pkg/handlers/opensearch"
	"github.com/nais/aivenator/pkg/handlers/secret"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
)

type Handler interface {
	Apply(application *aiven_nais_io_v1.AivenApplication, rs *appsv1.ReplicaSet, secret *v1.Secret, logger *log.Entry) error
	Cleanup(secret *v1.Secret, logger *log.Entry) error
}

type Manager struct {
	handlers []Handler
}

func NewManager(ctx context.Context, aiven *aiven.Client, kafkaProjects []string, mainProjectName string, logger *log.Entry) Manager {
	return Manager{
		handlers: []Handler{
			secret.Handler{},
			kafka.NewKafkaHandler(ctx, aiven, kafkaProjects, logger),
			opensearch.NewOpenSearchHandler(aiven, mainProjectName),
		},
	}
}

func (c Manager) CreateSecret(application *aiven_nais_io_v1.AivenApplication, rs *appsv1.ReplicaSet, secret *v1.Secret, logger *log.Entry) (*v1.Secret, error) {
	for _, handler := range c.handlers {
		err := handler.Apply(application, rs, secret, logger)
		if err != nil {
			cleanupError := c.Cleanup(secret, logger)
			if cleanupError != nil {
				return nil, fmt.Errorf("error during apply: %w, additionally, an error occured during cleanup: %v", err, cleanupError)
			}
			return nil, err
		}
	}
	return secret, nil
}

func (c Manager) Cleanup(s *v1.Secret, logger *log.Entry) error {
	for _, handler := range c.handlers {
		err := handler.Cleanup(s, logger)
		if err != nil {
			return err
		}
	}
	return nil
}
