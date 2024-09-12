package service

import (
	"context"
	"fmt"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/metrics"
)

type ServiceManager interface {
	Get(ctx context.Context, projectName, serviceName string) (*aiven.Service, error)
	GetServiceAddresses(ctx context.Context, projectName, serviceName string) (*ServiceAddresses, error)
}

type Manager struct {
	service      *aiven.ServicesHandler
	addressCache map[cacheKey]*ServiceAddresses
}

type cacheKey struct {
	projectName string
	serviceName string
}

type ServiceAddress struct {
	URI  string
	Host string
	Port int
}

type ServiceAddresses struct {
	ServiceURI     string
	SchemaRegistry *ServiceAddress
	OpenSearch     *ServiceAddress
	Redis          *ServiceAddress
	InfluxDB       *ServiceAddress
}

func NewManager(service *aiven.ServicesHandler) ServiceManager {
	return &Manager{
		service:      service,
		addressCache: map[cacheKey]*ServiceAddresses{},
	}
}

func (r *Manager) GetServiceAddresses(ctx context.Context, projectName, serviceName string) (*ServiceAddresses, error) {
	var addresses *ServiceAddresses
	var ok bool
	key := cacheKey{
		projectName: projectName,
		serviceName: serviceName,
	}
	if addresses, ok = r.addressCache[key]; !ok {
		aivenService, err := r.Get(ctx, projectName, serviceName)
		if err != nil {
			return nil, err
		}
		addresses = &ServiceAddresses{
			ServiceURI:     getServiceURI(aivenService),
			SchemaRegistry: getServiceAddress(aivenService, "schema_registry", "https"),
			OpenSearch:     getServiceAddress(aivenService, "opensearch", "https"),
			Redis:          getServiceAddress(aivenService, "redis", "rediss"),
			InfluxDB:       getServiceAddress(aivenService, "influxdb", "https+influxdb"),
		}
		r.addressCache[key] = addresses
	}
	return addresses, nil
}

func (r *Manager) Get(ctx context.Context, projectName, serviceName string) (*aiven.Service, error) {
	var service *aiven.Service
	err := metrics.ObserveAivenLatency("Service_Get", projectName, func() error {
		var err error
		service, err = r.service.Get(ctx, projectName, serviceName)
		return err
	})
	return service, err
}

func getServiceURI(service *aiven.Service) string {
	return service.URI
}

func getServiceAddress(service *aiven.Service, componentName, scheme string) *ServiceAddress {
	component := findComponent(componentName, service.Components)
	if component != nil {
		return &ServiceAddress{
			URI:  fmt.Sprintf("%s://%s:%d", scheme, component.Host, component.Port),
			Host: component.Host,
			Port: component.Port,
		}
	}
	return nil
}

func findComponent(needle string, haystack []*aiven.ServiceComponents) *aiven.ServiceComponents {
	for _, c := range haystack {
		if c.Component == needle {
			return c
		}
	}
	return nil
}
