package service

import (
	"context"
	"fmt"
	"time"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/metrics"
)

const serviceAddressCacheTTL = 1 * time.Hour

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
	SchemaRegistry ServiceAddress
	OpenSearch     ServiceAddress
	Redis          ServiceAddress
	InfluxDB       ServiceAddress
	expires        time.Time
}

func NewManager(service *aiven.ServicesHandler) ServiceManager {
	return &Manager{
		service:      service,
		addressCache: map[cacheKey]*ServiceAddresses{},
	}
}

func (r *Manager) GetServiceAddresses(ctx context.Context, projectName, serviceName string) (*ServiceAddresses, error) {
	addresses, err := r.getServiceAddressesFromCache(projectName, serviceName)
	if err != nil {
		_, err = r.Get(ctx, projectName, serviceName)
		if err != nil {
			return nil, err
		}
		return r.getServiceAddressesFromCache(projectName, serviceName)
	}
	return addresses, nil
}

func (r *Manager) getServiceAddressesFromCache(projectName, serviceName string) (*ServiceAddresses, error) {
	key := cacheKey{
		projectName: projectName,
		serviceName: serviceName,
	}
	addresses, ok := r.addressCache[key]
	if !ok {
		return nil, fmt.Errorf("service addresses not found in cache")
	}
	if addresses.expires.Before(time.Now()) {
		return nil, fmt.Errorf("service addresses expired")
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
	key := cacheKey{
		projectName: projectName,
		serviceName: serviceName,
	}
	addresses := &ServiceAddresses{
		ServiceURI:     getServiceURI(service),
		SchemaRegistry: getServiceAddress(service, "schema_registry", "https"),
		OpenSearch:     getServiceAddress(service, "opensearch", "https"),
		Redis:          getServiceAddress(service, "redis", "rediss"),
		InfluxDB:       getServiceAddress(service, "influxdb", "https+influxdb"),
		expires:        time.Now().Add(serviceAddressCacheTTL),
	}
	r.addressCache[key] = addresses
	return service, err
}

func getServiceURI(service *aiven.Service) string {
	return service.URI
}

func getServiceAddress(service *aiven.Service, componentName, scheme string) ServiceAddress {
	component := findComponent(componentName, service.Components)
	if component != nil {
		return ServiceAddress{
			URI:  fmt.Sprintf("%s://%s:%d", scheme, component.Host, component.Port),
			Host: component.Host,
			Port: component.Port,
		}
	}
	return ServiceAddress{}
}

func findComponent(needle string, haystack []*aiven.ServiceComponents) *aiven.ServiceComponents {
	for _, c := range haystack {
		if c.Component == needle {
			return c
		}
	}
	return nil
}
