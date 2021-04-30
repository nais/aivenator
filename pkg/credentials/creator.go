package credentials

import (
	"fmt"
	"github.com/nais/aivenator/pkg/credentials/kafka"
	kafka_nais_io_v1 "github.com/nais/liberator/pkg/apis/kafka.nais.io/v1"
	"k8s.io/api/core/v1"
)

type Handler interface {
	Apply(application *kafka_nais_io_v1.AivenApplication, secret *v1.Secret) error
}

type Creator struct {
	handlers []Handler
}

func NewCreator() Creator {
	return Creator{
		handlers: []Handler{
			kafka.KafkaHandler{},
		},
	}
}

func (c Creator) Apply(application *kafka_nais_io_v1.AivenApplication, secret *v1.Secret) error {
	for _, handler := range c.handlers {
		err := handler.Apply(application, secret)
		if err != nil {
			return fmt.Errorf("failed to apply resource creation: %v", err)
		}
	}
	return nil
}
