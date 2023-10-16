package project

import (
	"context"
	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/metrics"
)

type ProjectManager interface {
	GetCA(ctx context.Context, projectName string) (string, error)
}

type Manager struct {
	ca *aiven.CAHandler
}

func NewManager(ca *aiven.CAHandler) ProjectManager {
	return &Manager{
		ca: ca,
	}
}

func (r *Manager) GetCA(ctx context.Context, projectName string) (string, error) {
	var ca string
	err := metrics.ObserveAivenLatency("CA_Get", projectName, func() error {
		var err error
		ca, err = r.ca.Get(ctx, projectName)
		return err
	})
	return ca, err
}
