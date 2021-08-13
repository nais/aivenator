package project

import (
	"github.com/aiven/aiven-go-client"
	"github.com/nais/aivenator/pkg/metrics"
)

type ProjectManager interface {
	GetCA(projectName string) (string, error)
}

type Manager struct {
	ca *aiven.CAHandler
}

func NewManager(ca *aiven.CAHandler) ProjectManager {
	return &Manager{
		ca: ca,
	}
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
