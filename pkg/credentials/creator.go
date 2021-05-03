package credentials

import (
	"fmt"
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
		handlers: []Handler{},
	}
}

func (c Creator) CreateSecret(application *kafka_nais_io_v1.AivenApplication) (*v1.Secret, error) {
	secret := v1.Secret{}
	for _, handler := range c.handlers {
		err := handler.Apply(application, &secret)
		if err != nil {
			return nil, fmt.Errorf("failed to apply resource creation: %v", err)
		}
	}
	return &secret, nil
}
