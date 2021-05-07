package serviceuser

import (
	"github.com/aiven/aiven-go-client"
	"github.com/nais/aivenator/pkg/metrics"
)

type Interface interface {
	Create(project, service string, req aiven.CreateServiceUserRequest) (*aiven.ServiceUser, error)
	List(project, serviceName string) ([]*aiven.ServiceUser, error)
	Delete(project, service, user string) error
}

func NewManager(client Interface) *Manager {
	return &Manager{
		client: client,
	}
}

type Manager struct {
	client Interface
}

func (m Manager) Create(serviceUserName, projectName, serviceName string) (*aiven.ServiceUser, error) {
	req := aiven.CreateServiceUserRequest{
		Username: serviceUserName,
	}

	var aivenUser *aiven.ServiceUser
	err := metrics.ObserveAivenLatency("ServiceUser_Create", projectName, func() error {
		var err error
		aivenUser, err = m.client.Create(projectName, serviceName, req)
		return err
	})
	if err != nil {
		return nil, err
	}
	return aivenUser, nil
}
