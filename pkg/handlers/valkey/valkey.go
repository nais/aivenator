package valkey

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	redis "github.com/nais/aivenator/pkg/handlers/redis"
	"github.com/nais/aivenator/pkg/utils"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Annotations
const (
	ServiceUserAnnotation = "valkey.aiven.nais.io/serviceUser"
	ServiceNameAnnotation = "valkey.aiven.nais.io/serviceName"
	ProjectAnnotation     = "valkey.aiven.nais.io/project"
)

// Environment variables
const (
	ValkeyUser     = "VALKEY_USERNAME"
	ValkeyPassword = "VALKEY_PASSWORD"
	ValkeyURI      = "VALKEY_URI"
	ValkeyHost     = "VALKEY_HOST"
	ValkeyPort     = "VALKEY_PORT"
)

var namePattern = regexp.MustCompile("[^a-z0-9]")

func NewValkeyHandler(ctx context.Context, aiven *aiven.Client, projectName string) ValkeyHandler {
	return ValkeyHandler{
		serviceuser: serviceuser.NewManager(ctx, aiven.ServiceUsers),
		service:     service.NewManager(aiven.Services),
		projectName: projectName,
	}
}

type ValkeyHandler struct {
	serviceuser serviceuser.ServiceUserManager
	service     service.ServiceManager
	projectName string
}

func (h ValkeyHandler) Apply(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, secret *v1.Secret, logger log.FieldLogger) ([]*v1.Secret, error) {
	logger = logger.WithFields(log.Fields{"handler": "valkey"})
	if len(application.Spec.Valkey) == 0 {
		return nil, nil
	}

	for _, spec := range application.Spec.Valkey {
		serviceName := fmt.Sprintf("valkey-%s-%s", application.GetNamespace(), spec.Instance)

		logger = logger.WithFields(log.Fields{
			"project": h.projectName,
			"service": serviceName,
		})

		addresses, err := h.service.GetServiceAddresses(ctx, h.projectName, serviceName)
		if err != nil {
			return nil, utils.AivenFail("GetService", application, err, true, logger)
		}
		if len(addresses.Valkey.URI) == 0 {
			return nil, utils.AivenFail("GetService", application, fmt.Errorf("no Valkey service found"), true, logger)
		}

		aivenUser, err := h.provideServiceUser(ctx, application, spec, serviceName, secret, logger)
		if err != nil {
			return nil, err
		}

		serviceUserAnnotationKey := fmt.Sprintf("%s.%s", keyName(spec.Instance, "-"), ServiceUserAnnotation)
		serviceNameAnnotationKey := fmt.Sprintf("%s.%s", keyName(spec.Instance, "-"), ServiceNameAnnotation)

		secret.SetAnnotations(utils.MergeStringMap(secret.GetAnnotations(), map[string]string{
			serviceUserAnnotationKey: aivenUser.Username,
			serviceNameAnnotationKey: serviceName,
			ProjectAnnotation:        h.projectName,
		}))

		logger.Infof("Fetched service user %s", aivenUser.Username)

		envVarSuffix := envVarName(spec.Instance)
		secret.StringData = utils.MergeStringMap(secret.StringData, map[string]string{
			fmt.Sprintf("%s_%s", ValkeyUser, envVarSuffix):          aivenUser.Username,
			fmt.Sprintf("%s_%s", ValkeyPassword, envVarSuffix):      aivenUser.Password,
			fmt.Sprintf("%s_%s", ValkeyURI, envVarSuffix):           addresses.Valkey.URI,
			fmt.Sprintf("%s_%s", ValkeyHost, envVarSuffix):          addresses.Valkey.Host,
			fmt.Sprintf("%s_%s", ValkeyPort, envVarSuffix):          strconv.Itoa(addresses.Valkey.Port),
			fmt.Sprintf("%s_%s", redis.RedisPort, envVarSuffix):     strconv.Itoa(addresses.Valkey.Port),
			fmt.Sprintf("%s_%s", redis.RedisUser, envVarSuffix):     aivenUser.Username,
			fmt.Sprintf("%s_%s", redis.RedisPassword, envVarSuffix): aivenUser.Password,
			fmt.Sprintf("%s_%s", redis.RedisHost, envVarSuffix):     addresses.Valkey.Host,
			fmt.Sprintf("%s_%s", redis.RedisURI, envVarSuffix):      strings.Replace(addresses.Valkey.URI, "valkeys", "rediss", 1),
		})
	}

	controllerutil.AddFinalizer(secret, constants.AivenatorFinalizer)

	return []*v1.Secret{secret}, nil
}

func (h ValkeyHandler) provideServiceUser(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, valkeySpec *aiven_nais_io_v1.ValkeySpec, serviceName string, secret *v1.Secret, logger log.FieldLogger) (*aiven.ServiceUser, error) {
	var aivenUser *aiven.ServiceUser
	var err error

	var serviceUserName string

	if nameFromAnnotation, ok := secret.GetAnnotations()[ServiceUserAnnotation]; ok {
		serviceUserName = nameFromAnnotation
	} else {
		suffix, err := utils.CreateSuffix(application)
		if err != nil {
			err = fmt.Errorf("unable to create service user suffix: %s %w", err, utils.ErrUnrecoverable)
			utils.LocalFail("CreateSuffix", application, err, logger)
			return nil, err
		}

		serviceUserName = fmt.Sprintf("%s%s-%s", application.GetName(), utils.SelectSuffix(valkeySpec.Access), suffix)
	}

	aivenUser, err = h.serviceuser.Get(ctx, serviceUserName, h.projectName, serviceName, logger)
	if err == nil {
		return aivenUser, nil
	}
	if !aiven.IsNotFound(err) {
		return nil, utils.AivenFail("GetServiceUser", application, err, false, logger)
	}
	accessControl := &aiven.AccessControl{
		ValkeyACLCategories: getValkeyACLCategories(valkeySpec.Access),
		ValkeyACLKeys:       []string{"*"},
		ValkeyACLChannels:   []string{"*"},
	}

	aivenUser, err = h.serviceuser.Create(ctx, serviceUserName, h.projectName, serviceName, accessControl, logger)
	if err != nil {
		return nil, utils.AivenFail("CreateServiceUser", application, err, false, logger)
	}
	return aivenUser, nil
}

func keyName(instanceName, replacement string) string {
	return namePattern.ReplaceAllString(instanceName, replacement)
}

func envVarName(instanceName string) string {
	return strings.ToUpper(keyName(instanceName, "_"))
}

func getValkeyACLCategories(access string) []string {
	categories := make([]string, 0, 7)
	categories = append(categories, "-@all", "+@connection", "+@scripting", "+@pubsub", "+@transaction")
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
	return categories
}

func (h ValkeyHandler) Cleanup(ctx context.Context, secret *v1.Secret, logger *log.Entry) error {
	annotations := secret.GetAnnotations()
	projectName, okProjectName := annotations[ProjectAnnotation]

	logger = logger.WithFields(log.Fields{"project": projectName})
	for annotationKey := range annotations {
		// Specifically for the suffix serviceName
		if strings.HasSuffix(annotationKey, ServiceNameAnnotation) {
			serviceName := annotations[annotationKey]
			logger = logger.WithField("service", serviceName)
			instance := strings.Split(annotationKey, ".")[0]

			serviceUserNameKey := fmt.Sprintf("%s.%s", instance, ServiceUserAnnotation)
			serviceUserName, okServiceUser := annotations[serviceUserNameKey]
			if !okServiceUser {
				logger.Errorf("missing annotation %s", serviceUserNameKey)
				continue
			}

			if !okProjectName {
				return fmt.Errorf("missing annotation %s", ProjectAnnotation)
			}

			if err := h.serviceuser.Delete(ctx, serviceUserName, projectName, serviceName, logger); err != nil {
				if aiven.IsNotFound(err) {
					logger.Infof("Service user %s does not exist", serviceUserName)
					continue
				}

				logger.Errorf("deleting service user %s: %v", serviceUserName, err)
				continue
			}

			logger.Infof("Deleted service user %s", serviceUserName)
		}
	}

	return nil
}
