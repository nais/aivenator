package serviceuser

import (
	"github.com/aiven/aiven-go-client"
	"github.com/nais/aivenator/pkg/metrics"
)

func NewManager(serviceUsers *aiven.ServiceUsersHandler) ServiceUserManager {
	return &Manager{
		serviceUsers: serviceUsers,
	}
}

type ServiceUserManager interface {
	Create(serviceUserName, projectName, serviceName string) (*aiven.ServiceUser, error)
}

type Manager struct {
	serviceUsers *aiven.ServiceUsersHandler
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
	return aivenUser, nil
}
