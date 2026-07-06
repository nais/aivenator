package service

import (
	"context"
	"fmt"
	"time"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/metrics"
)

const serviceAddressCacheTTL = 5 * time.Minute

type ServiceManager interface {
	Get(ctx context.Context, projectName, serviceName string) (*aiven.Service, error)
	GetServiceAddressesFromCache(ctx context.Context, projectName, serviceName string) (ServiceAddresses, error)
	GetServiceAddresses(ctx context.Context, projectName, serviceName string) (ServiceAddresses, error)
}

type Manager struct {
	service      *aiven.ServicesHandler
	addressCache map[cacheKey]*serviceAddresses
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

type ServiceAddresses interface {
	Kafka() ServiceAddress
	SchemaRegistry() ServiceAddress
	OpenSearch() ServiceAddress
	OpenSearchDashboard() ServiceAddress
	Valkey() ServiceAddress
	ValkeyReplica() ServiceAddress
}

type serviceAddresses struct {
	kafka               ServiceAddress
	schemaRegistry      ServiceAddress
	openSearch          ServiceAddress
	openSearchDashboard ServiceAddress
	valkey              ServiceAddress
	valkeyReplica       ServiceAddress
	expires             time.Time
}

func (s *serviceAddresses) Kafka() ServiceAddress {
	return s.kafka
}

func (s *serviceAddresses) SchemaRegistry() ServiceAddress {
	return s.schemaRegistry
}

func (s *serviceAddresses) OpenSearch() ServiceAddress {
	return s.openSearch
}

func (s *serviceAddresses) OpenSearchDashboard() ServiceAddress {
	return s.openSearchDashboard
}

func (s *serviceAddresses) Valkey() ServiceAddress {
	return s.valkey
}

func (s *serviceAddresses) ValkeyReplica() ServiceAddress {
	return s.valkeyReplica
}

func NewManager(service *aiven.ServicesHandler) ServiceManager {
	return &Manager{
		service:      service,
		addressCache: map[cacheKey]*serviceAddresses{},
	}
}

func (r *Manager) GetServiceAddresses(ctx context.Context, projectName, serviceName string) (ServiceAddresses, error) {
	_, err := r.Get(ctx, projectName, serviceName)
	if err != nil {
		return nil, err
	}
	return r.getServiceAddressesFromCache(projectName, serviceName)
}

func (r *Manager) GetServiceAddressesFromCache(ctx context.Context, projectName, serviceName string) (ServiceAddresses, error) {
	addresses, err := r.getServiceAddressesFromCache(projectName, serviceName)
	if err != nil {
		return r.GetServiceAddresses(ctx, projectName, serviceName)
	}
	return addresses, nil
}

func (r *Manager) getServiceAddressesFromCache(projectName, serviceName string) (*serviceAddresses, error) {
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
	if err != nil {
		return nil, err
	}
	key := cacheKey{
		projectName: projectName,
		serviceName: serviceName,
	}
	addresses := &serviceAddresses{
		kafka:               getPrimaryServiceAddress(service, "kafka", ""),
		schemaRegistry:      getPrimaryServiceAddress(service, "schema_registry", "https"),
		openSearch:          getPrimaryServiceAddress(service, "opensearch", "https"),
		openSearchDashboard: getPrimaryServiceAddress(service, "opensearch_dashboards", "https"),
		valkey:              getPrimaryServiceAddress(service, "valkey", "valkeys"),
		valkeyReplica:       getServiceAddress(service, "valkey", "valkeys", "replica"),
		expires:             time.Now().Add(serviceAddressCacheTTL),
	}
	r.addressCache[key] = addresses
	return service, err
}

func getPrimaryServiceAddress(service *aiven.Service, componentName, scheme string) ServiceAddress {
	return getServiceAddress(service, componentName, scheme, "primary")
}

func getServiceAddress(service *aiven.Service, componentName string, scheme string, usage string) ServiceAddress {
	component := findComponent(componentName, usage, service.Components)
	if component != nil {
		uri := fmt.Sprintf("%s://%s:%d", scheme, component.Host, component.Port)
		if scheme == "" {
			uri = fmt.Sprintf("%s:%d", component.Host, component.Port)
		}
		return ServiceAddress{
			URI:  uri,
			Host: component.Host,
			Port: component.Port,
		}
	}
	return ServiceAddress{}
}

func findComponent(needle, usage string, haystack []*aiven.ServiceComponents) *aiven.ServiceComponents {
	for _, c := range haystack {
		if c.Component == needle && c.Usage == usage {
			return c
		}
	}
	return nil
}
