package service

import (
	"fmt"
	"github.com/aiven/aiven-go-client"
	"github.com/nais/aivenator/pkg/metrics"
)

type ServiceManager interface {
	Get(projectName, serviceName string) (*aiven.Service, error)
	GetServiceAddresses(projectName, serviceName string) (*ServiceAddresses, error)
}

type Manager struct {
	service      *aiven.ServicesHandler
	addressCache map[cacheKey]*ServiceAddresses
}

type cacheKey struct {
	projectName string
	serviceName string
}

type ServiceAddresses struct {
	ServiceURI     string
	SchemaRegistry string
	ElasticSearch  string
}

func NewManager(service *aiven.ServicesHandler) ServiceManager {
	return &Manager{
		service:      service,
		addressCache: map[cacheKey]*ServiceAddresses{},
	}
}

func (r *Manager) GetServiceAddresses(projectName, serviceName string) (*ServiceAddresses, error) {
	var addresses *ServiceAddresses
	var ok bool
	key := cacheKey{
		projectName: projectName,
		serviceName: serviceName,
	}
	if addresses, ok = r.addressCache[key]; !ok {
		aivenService, err := r.Get(projectName, serviceName)
		if err != nil {
			return nil, err
		}
		addresses = &ServiceAddresses{
			ServiceURI:     GetServiceURI(aivenService),
			ElasticSearch:  GetElasticSearchAddress(aivenService),
			SchemaRegistry: GetSchemaRegistryAddress(aivenService),
		}
		r.addressCache[key] = addresses
	}
	return addresses, nil
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

func GetServiceURI(service *aiven.Service) string {
	return service.URI
}

func GetSchemaRegistryAddress(service *aiven.Service) string {
	schemaRegistryComponent := findComponent("schema_registry", service.Components)
	if schemaRegistryComponent != nil {
		return fmt.Sprintf("https://%s:%d", schemaRegistryComponent.Host, schemaRegistryComponent.Port)
	}
	return ""
}

func GetElasticSearchAddress(service *aiven.Service) string {
	elasticComponent := findComponent("elasticsearch", service.Components)
	if elasticComponent != nil {
		return fmt.Sprintf("https://%s:%d", elasticComponent.Host, elasticComponent.Port)
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
