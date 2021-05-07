package credentials

import (
	"github.com/aiven/aiven-go-client"
	"github.com/nais/aivenator/pkg/handlers/kafka"
	"github.com/nais/aivenator/pkg/handlers/secret"
	kafka_nais_io_v1 "github.com/nais/liberator/pkg/apis/kafka.nais.io/v1"
	log "github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
)

type Handler interface {
	Apply(application *kafka_nais_io_v1.AivenApplication, secret *v1.Secret, logger *log.Entry) error
}

type Creator struct {
	handlers []Handler
}

func NewCreator(aiven *aiven.Client) Creator {
	return Creator{
		handlers: []Handler{
			secret.Handler{},
			kafka.NewKafkaHandler(aiven),
		},
	}
}

func (c Creator) CreateSecret(application *kafka_nais_io_v1.AivenApplication, logger *log.Entry) (*v1.Secret, error) {
	s := v1.Secret{}
	for _, handler := range c.handlers {
		err := handler.Apply(application, &s, logger)
		if err != nil {
			return nil, err
		}
	}
	return &s, nil
}
