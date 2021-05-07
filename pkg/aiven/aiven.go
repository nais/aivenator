package aiven

import (
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
)

const (
	MaxServiceUserNameLength = 64
	ServiceUserAnnotation    = "aiven.kafka.nais.io/serviceUser"
)

type Interfaces struct {
	CA           service.CA
	Service      service.Interface
	ServiceUsers serviceuser.Interface
}

func DefaultKafkaService(project string) string {
	return project + "-kafka"
}
