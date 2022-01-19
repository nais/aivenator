package serviceuser

import (
	"github.com/aiven/aiven-go-client"
	"github.com/nais/aivenator/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"strings"
)

const (
	dotSeparator        = "dot"
	underscoreSeparator = "underscore"
)

func NewManager(serviceUsers *aiven.ServiceUsersHandler) ServiceUserManager {
	return &Manager{
		serviceUsers: serviceUsers,
	}
}

type ServiceUserManager interface {
	Create(serviceUserName, projectName, serviceName string, logger *log.Entry) (*aiven.ServiceUser, error)
	Get(serviceUserName, projectName, serviceName string, logger *log.Entry) (*aiven.ServiceUser, error)
	Delete(serviceUserName, projectName, serviceName string, logger *log.Entry) error
	ObserveServiceUsersCount(projectName, serviceName string, logger *log.Entry)
}

type Manager struct {
	serviceUsers *aiven.ServiceUsersHandler
}

func (m *Manager) ObserveServiceUsersCount(projectName, serviceName string, logger *log.Entry) {
	list, err := m.serviceUsers.List(projectName, serviceName)
	if err != nil {
		logger.Errorf("not able to fetch service users list: %s", err)
	} else {
		dotSeparatorCount, underscoreSeparatorCound := countUsers(list)
		metrics.ServiceUsersCount.WithLabelValues(projectName, dotSeparator).Set(float64(dotSeparatorCount))
		metrics.ServiceUsersCount.WithLabelValues(projectName, underscoreSeparator).Set(float64(underscoreSeparatorCound))
	}
}

func countUsers(list []*aiven.ServiceUser) (int, int) {
	dotCount := 0
	underscoreCount := 0
	for _, user := range list {
		if strings.Count(user.Username, "_") == 3 {
			underscoreCount++
		} else if strings.Count(user.Username, ".") >= 1 {
			dotCount++
		}
	}
	return dotCount, underscoreCount
}

func (m *Manager) Get(serviceUserName, projectName, serviceName string, logger *log.Entry) (*aiven.ServiceUser, error) {
	var aivenUser *aiven.ServiceUser
	err := metrics.ObserveAivenLatency("ServiceUser_Get", projectName, func() error {
		var err error
		aivenUser, err = m.serviceUsers.Get(projectName, serviceName, serviceUserName)
		return err
	})
	if err != nil {
		return nil, err
	}
	m.ObserveServiceUsersCount(projectName, serviceName, logger)
	return aivenUser, nil
}

func (m *Manager) Delete(serviceUserName, projectName, serviceName string, logger *log.Entry) error {
	err := metrics.ObserveAivenLatency("ServiceUser_Delete", projectName, func() error {
		var err error
		err = m.serviceUsers.Delete(projectName, serviceName, serviceUserName)
		return err
	})
	if err != nil {
		return err
	}
	metrics.ServiceUsersDeleted.With(prometheus.Labels{metrics.LabelPool: projectName}).Inc()
	m.ObserveServiceUsersCount(projectName, serviceName, logger)
	return nil
}

func (m *Manager) Create(serviceUserName, projectName, serviceName string, logger *log.Entry) (*aiven.ServiceUser, error) {
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
	m.ObserveServiceUsersCount(projectName, serviceName, logger)
	return aivenUser, nil
}
