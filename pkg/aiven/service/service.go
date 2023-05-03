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
	OpenSearch     string
	Redis          string
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
			ServiceURI:     getServiceURI(aivenService),
			SchemaRegistry: getServiceAddress(aivenService, "schema_registry", "https"),
			OpenSearch:     getServiceAddress(aivenService, "opensearch", "https"),
			Redis:          getServiceAddress(aivenService, "redis", "rediss"),
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

func getServiceURI(service *aiven.Service) string {
	return service.URI
}

func getServiceAddress(service *aiven.Service, componentName, scheme string) string {
	component := findComponent(componentName, service.Components)
	if component != nil {
		return fmt.Sprintf("%s://%s:%d", scheme, component.Host, component.Port)
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
