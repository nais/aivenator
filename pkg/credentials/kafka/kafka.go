package kafka

import (
	kafka_nais_io_v1 "github.com/nais/liberator/pkg/apis/kafka.nais.io/v1"
	"k8s.io/api/core/v1"
)

type KafkaHandler struct {
}

func (h KafkaHandler) Apply(application *kafka_nais_io_v1.AivenApplication, secret *v1.Secret) error {
	return nil
}
