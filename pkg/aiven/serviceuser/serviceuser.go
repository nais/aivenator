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
	Delete(serviceUserName, projectName, serviceName string) error
}

type Manager struct {
	serviceUsers *aiven.ServiceUsersHandler
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
	return nil
}

func (m *Manager) Create(serviceUserName, projectName, serviceName string) (*aiven.ServiceUser, error) {
	req := aiven.CreateServiceUserRequest{
		Username: serviceUserName,
		// XXX: Needed because of https://github.com/aiven/aiven-go-client/issues/111
		AccessControl: aiven.AccessControl{
			RedisACLCategories: []string{},
			RedisACLCommands:   []string{},
			RedisACLKeys:       []string{},
		},
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
	return aivenUser, nil
}
