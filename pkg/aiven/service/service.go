package service

import (
	"fmt"
	"github.com/aiven/aiven-go-client"
	"github.com/nais/aivenator/pkg/metrics"
)

func NewManager(service *aiven.ServicesHandler, ca *aiven.CAHandler) ServiceManager {
	return &Manager{
		service: service,
		ca:      ca,
	}
}

type ServiceManager interface {
	Get(projectName, serviceName string) (*aiven.Service, error)
	GetCA(projectName string) (string, error)
}

type Manager struct {
	service *aiven.ServicesHandler
	ca      *aiven.CAHandler
}

func (r *Manager) Get(projectName, serviceName string) (*aiven.Service, error) {
	var service *aiven.Service
	err := metrics.ObserveAivenLatency("Service_Get", projectName, func() error {
		var err error
		service, err = r.service.Get(projectName, serviceName)
		return err
	})
	return service, err
}

func (r *Manager) GetCA(projectName string) (string, error) {
	var ca string
	err := metrics.ObserveAivenLatency("CA_Get", projectName, func() error {
		var err error
		ca, err = r.ca.Get(projectName)
		return err
	})
	return ca, err
}

func GetKafkaBrokerAddress(service *aiven.Service) string {
	return service.URI
}

func GetSchemaRegistryAddress(service *aiven.Service) string {
	schemaRegistryComponent := findComponent("schema_registry", service.Components)
	if schemaRegistryComponent != nil {
		return fmt.Sprintf("https://%s:%d", schemaRegistryComponent.Host, schemaRegistryComponent.Port)
	}
	return ""
}

func findComponent(needle string, haystack []*aiven.ServiceComponents) *aiven.ServiceComponents {
	for _, c := range haystack {
		if c.Component == needle {
			return c
		}
	}
	return nil
}
