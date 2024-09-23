package serviceuser

import (
	"context"
	"fmt"
	"strings"
	"time"

	cache "github.com/Code-Hex/go-generics-cache"
	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

const (
	dotSeparator        = "dot"
	underscoreSeparator = "underscore"
	other               = "other"

	cacheExpiration = 5 * time.Minute
)

type cacheKey struct {
	projectName     string
	serviceName     string
	serviceUserName string
}

func NewManager(ctx context.Context, serviceUsers *aiven.ServiceUsersHandler) ServiceUserManager {
	return &Manager{
		serviceUsers:     serviceUsers,
		serviceUserCache: cache.NewContext[cacheKey, *aiven.ServiceUser](ctx),
	}
}

type ServiceUserManager interface {
	Create(ctx context.Context, serviceUserName, projectName, serviceName string, accessControl *aiven.AccessControl, logger log.FieldLogger) (*aiven.ServiceUser, error)
	Get(ctx context.Context, serviceUserName, projectName, serviceName string, logger log.FieldLogger) (*aiven.ServiceUser, error)
	Delete(ctx context.Context, serviceUserName, projectName, serviceName string, logger log.FieldLogger) error
	ObserveServiceUsersCount(ctx context.Context, projectName, serviceName string, logger log.FieldLogger)
	GetCacheExpiration() time.Duration
}

type Manager struct {
	serviceUsers     *aiven.ServiceUsersHandler
	serviceUserCache *cache.Cache[cacheKey, *aiven.ServiceUser]
}

type userCount struct {
	dot        int
	underscore int
	other      int
}

func (m *Manager) GetCacheExpiration() time.Duration {
	return cacheExpiration
}

func (m *Manager) ObserveServiceUsersCount(ctx context.Context, projectName, serviceName string, logger log.FieldLogger) {
	var users []*aiven.ServiceUser
	err := metrics.ObserveAivenLatency("ServiceUser_List", projectName, func() error {
		var err error
		users, err = m.serviceUsers.List(ctx, projectName, serviceName)
		return err
	})
	if err != nil {
		logger.Errorf("not able to fetch service users users: %s", err)
	} else {
		counts := m.countUsersAndUpdateCache(projectName, serviceName, users)
		metrics.ServiceUsersCount.WithLabelValues(projectName, dotSeparator).Set(float64(counts.dot))
		metrics.ServiceUsersCount.WithLabelValues(projectName, underscoreSeparator).Set(float64(counts.underscore))
		metrics.ServiceUsersCount.WithLabelValues(projectName, other).Set(float64(counts.other))
	}
}

func (m *Manager) countUsersAndUpdateCache(projectName, serviceName string, users []*aiven.ServiceUser) userCount {
	counts := userCount{}
	for _, user := range users {
		if strings.Count(user.Username, "_") == 3 {
			counts.underscore++
		} else if strings.Count(user.Username, ".") >= 1 {
			counts.dot++
		} else {
			counts.other++
		}
		m.serviceUserCache.Set(cacheKey{projectName, serviceName, user.Username}, user, cache.WithExpiration(m.GetCacheExpiration()))
	}
	return counts
}

func (m *Manager) Get(ctx context.Context, serviceUserName, projectName, serviceName string, logger log.FieldLogger) (*aiven.ServiceUser, error) {
	key := cacheKey{projectName, serviceName, serviceUserName}
	if val, found := m.serviceUserCache.Get(key); found {
		logger.Debugf("serviceUserCache hit for %v", key)
		return val, nil
	}
	logger.Debugf("serviceUserCache miss for %v", key)

	// serviceUsers.Get does a List internally anyway (there is no API for getting just one), so we explicitly call List
	// to make it clear what is going on.
	// Since we're getting all the users, we put them in the cache for later so that we don't have to call List on every "Get".
	var aivenUsers []*aiven.ServiceUser
	err := metrics.ObserveAivenLatency("ServiceUser_List", projectName, func() error {
		var err error
		aivenUsers, err = m.serviceUsers.List(ctx, projectName, serviceName)
		return err
	})
	if err != nil {
		return nil, err
	}

	var aivenUser *aiven.ServiceUser
	for _, u := range aivenUsers {
		m.serviceUserCache.Set(cacheKey{projectName, serviceName, u.Username}, u, cache.WithExpiration(cacheExpiration))
		if u.Username == serviceUserName {
			aivenUser = u
		}
	}
	if aivenUser == nil {
		return nil, aiven.Error{
			Message: fmt.Sprintf("ServiceUser '%s' not found", serviceUserName),
			Status:  404,
		}
	}

	return aivenUser, nil
}

func (m *Manager) Delete(ctx context.Context, serviceUserName, projectName, serviceName string, logger log.FieldLogger) error {
	err := metrics.ObserveAivenLatency("ServiceUser_Delete", projectName, func() error {
		return m.serviceUsers.Delete(ctx, projectName, serviceName, serviceUserName)
	})
	if err != nil {
		return err
	}
	m.serviceUserCache.Delete(cacheKey{projectName, serviceName, serviceUserName})
	metrics.ServiceUsersDeleted.With(prometheus.Labels{metrics.LabelPool: projectName}).Inc()
	m.ObserveServiceUsersCount(ctx, projectName, serviceName, logger)
	return nil
}

func (m *Manager) Create(ctx context.Context, serviceUserName, projectName, serviceName string, accessControl *aiven.AccessControl, logger log.FieldLogger) (*aiven.ServiceUser, error) {
	req := aiven.CreateServiceUserRequest{
		Username:      serviceUserName,
		AccessControl: accessControl,
	}

	var aivenUser *aiven.ServiceUser
	err := metrics.ObserveAivenLatency("ServiceUser_Create", projectName, func() error {
		var err error
		aivenUser, err = m.serviceUsers.Create(ctx, projectName, serviceName, req)
		return err
	})
	if err != nil {
		return nil, err
	}
	metrics.ServiceUsersCreated.With(prometheus.Labels{metrics.LabelPool: projectName}).Inc()
	m.ObserveServiceUsersCount(ctx, projectName, serviceName, logger)
	return aivenUser, nil
}
