package serviceuser

import (
	"github.com/aiven/aiven-go-client"
	"github.com/nais/aivenator/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

func NewManager(serviceUsers *aiven.ServiceUsersHandler) ServiceUserManager {
	return &Manager{
		serviceUsers: serviceUsers,
	}
}

type ServiceUserManager interface {
	Create(serviceUserName, projectName, serviceName string) (*aiven.ServiceUser, error)
	Get(serviceUserName, projectName, serviceName string) (*aiven.ServiceUser, error)
	Delete(serviceUserName, projectName, serviceName string) error
	ObserveServiceUsersCount(projectName, serviceName string) error
}

type Manager struct {
	serviceUsers *aiven.ServiceUsersHandler
}

func (m *Manager) ObserveServiceUsersCount(projectName, serviceName string) error {
	list, err := m.serviceUsers.List(projectName, serviceName)
	if err != nil {
		return err
	}
	count := len(list)
	metrics.ServiceUsersCount.WithLabelValues(projectName).Set(float64(count))
	return nil
}

func (m *Manager) Get(serviceUserName, projectName, serviceName string) (*aiven.ServiceUser, error) {
	var aivenUser *aiven.ServiceUser
	err := metrics.ObserveAivenLatency("ServiceUser_Get", projectName, func() error {
		var err error
		aivenUser, err = m.serviceUsers.Get(projectName, serviceName, serviceUserName)
		return err
	})
	if err != nil {
		return nil, err
	}
	err = m.ObserveServiceUsersCount(projectName, serviceName);
	if err != nil {
		return nil, err
	}
	return aivenUser, nil
}

func (m *Manager) Delete(serviceUserName, projectName, serviceName string) error {
	err := metrics.ObserveAivenLatency("ServiceUser_Delete", projectName, func() error {
		var err error
		err = m.serviceUsers.Delete(projectName, serviceName, serviceUserName)
		return err
	})
	if err != nil {
		return err
	}
	metrics.ServiceUsersDeleted.With(prometheus.Labels{metrics.LabelPool: projectName}).Inc()
	err = m.ObserveServiceUsersCount(projectName, serviceName);
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) Create(serviceUserName, projectName, serviceName string) (*aiven.ServiceUser, error) {
	req := aiven.CreateServiceUserRequest{
		Username: serviceUserName,
	}

	var aivenUser *aiven.ServiceUser
	err := metrics.ObserveAivenLatency("ServiceUser_Create", projectName, func() error {
		var err error
		aivenUser, err = m.serviceUsers.Create(projectName, serviceName, req)
		return err
	})
	if err != nil {
		return nil, err
	}
	metrics.ServiceUsersCreated.With(prometheus.Labels{metrics.LabelPool: projectName}).Inc()
	err = m.ObserveServiceUsersCount(projectName, serviceName);
	if err != nil {
		return nil, err
	}
	return aivenUser, nil
}