package redis

import (
	"context"
	"fmt"
	"github.com/aiven/aiven-go-client"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/utils"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
)

// Annotations
const (
	ServiceUserAnnotation = "redis.aiven.nais.io/serviceUser"
	ProjectAnnotation     = "redis.aiven.nais.io/project"
)

// Environment variables
const (
	RedisUser     = "REDIS_USERNAME"
	RedisPassword = "REDIS_PASSWORD"
	RedisURI      = "REDIS_URI"
)

func NewRedisHandler(ctx context.Context, aiven *aiven.Client, projectName string) RedisHandler {
	return RedisHandler{
		serviceuser: serviceuser.NewManager(ctx, aiven.ServiceUsers),
		service:     service.NewManager(aiven.Services),
		projectName: projectName,
	}
}

type RedisHandler struct {
	serviceuser serviceuser.ServiceUserManager
	service     service.ServiceManager
	projectName string
}

func (h RedisHandler) Apply(application *aiven_nais_io_v1.AivenApplication, secret *v1.Secret, logger log.FieldLogger) error {
	logger = logger.WithFields(log.Fields{"handler": "redis"})
	spec := application.Spec.Redis
	if spec == nil {
		return nil
	}

	serviceName := fmt.Sprintf("redis-%s-%s", application.GetNamespace(), spec.Instance)

	logger = logger.WithFields(log.Fields{
		"project": h.projectName,
		"service": serviceName,
	})

	addresses, err := h.service.GetServiceAddresses(h.projectName, serviceName)
	if err != nil {
		return utils.AivenFail("GetService", application, err, logger)
	}

	serviceUserName := fmt.Sprintf("%s%s", application.GetName(), utils.SelectSuffix(spec.Access))

	aivenUser, err := h.serviceuser.Get(serviceUserName, h.projectName, serviceName, logger)
	if err != nil {
		if aiven.IsNotFound(err) {
			accessControl := &aiven.AccessControl{
				RedisACLCategories: getRedisACLCategories(spec.Access),
				RedisACLKeys:       []string{"*"},
				RedisACLChannels:   []string{"*"},
			}
			aivenUser, err = h.serviceuser.Create(serviceUserName, h.projectName, serviceName, accessControl, logger)
			if err != nil {
				return utils.AivenFail("CreateServiceUser", application, err, logger)
			}
		} else {
			return utils.AivenFail("GetServiceUser", application, err, logger)
		}
	}

	secret.SetAnnotations(utils.MergeStringMap(secret.GetAnnotations(), map[string]string{
		ServiceUserAnnotation: aivenUser.Username,
		ProjectAnnotation:     h.projectName,
	}))
	logger.Infof("Fetched service user %s", aivenUser.Username)

	secret.StringData = utils.MergeStringMap(secret.StringData, map[string]string{
		RedisUser:     aivenUser.Username,
		RedisPassword: aivenUser.Password,
		RedisURI:      addresses.Redis,
	})

	return nil
}

func getRedisACLCategories(access string) []string {
	categories := make([]string, 0, 7)
	categories = append(categories, "-@all", "+@connection", "+@scripting", "+@pubsub")
	switch access {
	case "admin":
		categories = append(categories, "+@admin", "+@write", "+@read")
	case "readwrite":
		categories = append(categories, "+@write", "+@read")
	case "write":
		categories = append(categories, "+@write")
	default:
		categories = append(categories, "+@read")
	}
	categories = append(categories, "-@dangerous")
	return categories
}

func (h RedisHandler) Cleanup(_ *v1.Secret, _ *log.Entry) error {
	return nil
}
