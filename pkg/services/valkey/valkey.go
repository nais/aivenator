package valkey

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/constants"
	operator "github.com/nais/aivenator/pkg/aiven/operator"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/utils"
	aiven_io_v1alpha1 "github.com/nais/liberator/pkg/apis/aiven.io/v1alpha1"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Annotations
const (
	ServiceUserAnnotation       = "valkey.aiven.nais.io/serviceUser"
	ServiceNameAnnotation       = "valkey.aiven.nais.io/serviceName"
	ProjectAnnotation           = "valkey.aiven.nais.io/project"
	InstanceAnnotation          = "valkey.aiven.nais.io/instance"
	LegacyServiceUserAnnotation = "valkey.aiven.nais.io/legacyServiceUser"
)

// Environment variables
const (
	ValkeyUser        = "VALKEY_USERNAME"
	ValkeyPassword    = "VALKEY_PASSWORD"
	ValkeyURI         = "VALKEY_URI"
	ValkeyHost        = "VALKEY_HOST"
	ValkeyPort        = "VALKEY_PORT"
	ValkeyReplicaURI  = "VALKEY_REPLICA_URI"
	ValkeyReplicaHost = "VALKEY_REPLICA_HOST"
	ValkeyReplicaPort = "VALKEY_REPLICA_PORT"
	RedisUser         = "REDIS_USERNAME"
	RedisPassword     = "REDIS_PASSWORD"
	RedisURI          = "REDIS_URI"
	RedisHost         = "REDIS_HOST"
	RedisPort         = "REDIS_PORT"
)

var namePattern = regexp.MustCompile("[^a-z0-9]")

func NewValkeyHandler(ctx context.Context, aiven *aiven.Client, projectName string, k8sReader client.Reader, crServiceUser operator.ServiceUserManager) ValkeyHandler {
	return ValkeyHandler{
		crServiceUser: crServiceUser,
		k8sReader:     k8sReader,
		projectName:   projectName,
		secretConfig:  utils.NewSecretConfig(aiven, projectName),
		service:       service.NewManager(aiven.Services),
		serviceuser:   serviceuser.NewManager(ctx, aiven.ServiceUsers),
	}
}

type ValkeyHandler struct {
	crServiceUser operator.ServiceUserManager
	k8sReader     client.Reader
	projectName   string
	secretConfig  utils.SecretConfig
	service       service.ServiceManager
	// serviceuser deletes transitional pre-CR users; drop once the drain is done.
	serviceuser serviceuser.ServiceUserManager
}

func (h ValkeyHandler) Apply(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, logger log.FieldLogger) ([]corev1.Secret, error) {
	if len(application.Spec.Valkey) == 0 {
		return nil, nil
	}

	// One Secret object per instance is the contract with SaveSecret, whose update
	// is a wholesale replace: a duplicate name would silently drop every earlier
	// instance's credentials and cleanup annotations.
	seen := make(map[string]bool, len(application.Spec.Valkey))
	for _, valkeySpec := range application.Spec.Valkey {
		if seen[valkeySpec.SecretName] {
			err := fmt.Errorf("multiple valkey instances share secretName %q: %w", valkeySpec.SecretName, utils.ErrUnrecoverable)
			utils.LocalFail("ValidateSecretNames", application, err, logger)
			return nil, err
		}
		seen[valkeySpec.SecretName] = true
	}

	// Each instance is provisioned independently: one failing instance collects
	// its error but never discards the secrets of its healthy siblings.
	var secrets []corev1.Secret
	var errs []error
	for _, valkeySpec := range application.Spec.Valkey {
		secret, err := h.applyInstance(ctx, application, valkeySpec, logger)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		secrets = append(secrets, *secret)
	}

	return secrets, errors.Join(errs...)
}

func (h ValkeyHandler) applyInstance(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, valkeySpec *aiven_nais_io_v1.ValkeySpec, logger log.FieldLogger) (*corev1.Secret, error) {
	serviceName := fmt.Sprintf("valkey-%s-%s", application.GetNamespace(), valkeySpec.Instance)
	logger = logger.WithFields(log.Fields{
		"aivenProject":         h.projectName,
		"serviceName":          serviceName,
		"secretName":           valkeySpec.SecretName,
		"aivenServiceInstance": valkeySpec.Instance,
	})

	if _, err := utils.GetResourceInNamespace(ctx, h.k8sReader, &aiven_io_v1alpha1.Valkey{}, serviceName, application.GetNamespace()); err != nil {
		utils.LocalFail("ResolveValkeyInstance", application, err, logger)
		return nil, err
	}

	finalSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      valkeySpec.SecretName,
			Namespace: application.GetNamespace(),
		},
	}
	if _, err := h.secretConfig.ApplyIndividualSecret(ctx, application, finalSecret, logger); err != nil {
		return nil, utils.AivenFail("GetOrInitSecret", application, err, false, logger)
	}

	addresses, err := h.service.GetServiceAddresses(ctx, h.projectName, serviceName)
	if err != nil {
		return nil, utils.AivenFail("GetService", application, err, true, logger)
	}

	conn, err := h.resolveConnection(ctx, application, valkeySpec, serviceName, logger)
	if err != nil {
		return nil, err
	}
	logger = logger.WithField("serviceUser", conn.username)

	annotations := map[string]string{
		instanceAnnotation(valkeySpec.Instance, ServiceUserAnnotation): conn.username,
		instanceAnnotation(valkeySpec.Instance, ServiceNameAnnotation): serviceName,
		instanceAnnotation(valkeySpec.Instance, InstanceAnnotation):    valkeySpec.Instance,
		ProjectAnnotation: h.projectName,
	}
	if conn.legacyUsername != "" {
		annotations[instanceAnnotation(valkeySpec.Instance, LegacyServiceUserAnnotation)] = conn.legacyUsername
	}
	finalSecret.SetAnnotations(utils.MergeStringMap(finalSecret.GetAnnotations(), annotations))

	envVarSuffix := envVarName(valkeySpec.Instance)
	finalSecret.StringData = utils.MergeStringMap(finalSecret.StringData, map[string]string{
		fmt.Sprintf("%s_%s", ValkeyUser, envVarSuffix):     conn.username,
		fmt.Sprintf("%s_%s", ValkeyPassword, envVarSuffix): conn.password,
		fmt.Sprintf("%s_%s", ValkeyURI, envVarSuffix):      conn.uri,
		fmt.Sprintf("%s_%s", ValkeyHost, envVarSuffix):     conn.host,
		fmt.Sprintf("%s_%s", ValkeyPort, envVarSuffix):     conn.port,
		fmt.Sprintf("%s_%s", RedisUser, envVarSuffix):      conn.username,
		fmt.Sprintf("%s_%s", RedisPassword, envVarSuffix):  conn.password,
		fmt.Sprintf("%s_%s", RedisURI, envVarSuffix):       strings.Replace(conn.uri, "valkeys", "rediss", 1),
		fmt.Sprintf("%s_%s", RedisHost, envVarSuffix):      conn.host,
		fmt.Sprintf("%s_%s", RedisPort, envVarSuffix):      conn.port,
	})

	replicaServiceAddress := addresses.ValkeyReplica()
	if replicaServiceAddress.Port != 0 {
		finalSecret.StringData = utils.MergeStringMap(finalSecret.StringData, map[string]string{
			fmt.Sprintf("%s_%s", ValkeyReplicaURI, envVarSuffix):  replicaServiceAddress.URI,
			fmt.Sprintf("%s_%s", ValkeyReplicaHost, envVarSuffix): replicaServiceAddress.Host,
			fmt.Sprintf("%s_%s", ValkeyReplicaPort, envVarSuffix): strconv.Itoa(replicaServiceAddress.Port),
		})
	}

	controllerutil.AddFinalizer(finalSecret, constants.AivenatorFinalizer)
	logger.Infof("Applied individualSecret")
	return finalSecret, nil
}

// valkeyConnection holds the reprojected connection details for one Valkey instance.
type valkeyConnection struct {
	username string
	password string
	uri      string
	host     string
	port     string
	// legacyUsername is a pre-CR username abandoned because it cannot be a CR
	// name; carried on the secret so Cleanup deletes it when the secret drains.
	legacyUsername string
}

// resolveConnection provisions the service user via its ServiceUser CR and
// returns the connection details from the operator-published secret.
func (h ValkeyHandler) resolveConnection(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, valkeySpec *aiven_nais_io_v1.ValkeySpec, serviceName string, logger log.FieldLogger) (valkeyConnection, error) {
	res, err := h.resolveServiceUserName(ctx, application, valkeySpec, serviceName, logger)
	if err != nil {
		return valkeyConnection{}, utils.AivenFail("ResolveServiceUser", application, err, false, logger)
	}

	serviceUser, err := h.crServiceUser.CreateServiceUser(ctx, application, operator.ServiceUserSpec{
		Name:        res.Name,
		Namespace:   application.GetNamespace(),
		Project:     h.projectName,
		ServiceName: serviceName,
		AccessControl: &aiven_io_v1alpha1.ServiceUserAccessControl{
			ValkeyACLCategories: getValkeyACLCategories(valkeySpec.Access),
			ValkeyACLCommands:   []string{"+info", "+cluster|slots"},
			ValkeyACLKeys:       []string{"*"},
			ValkeyACLChannels:   []string{"*"},
		},
	}, logger)
	if err != nil {
		return valkeyConnection{}, utils.AivenFail("EnsureServiceUser", application, err, false, logger)
	}

	password, err := operator.Required(serviceUser.Secret, operator.ServiceUserPassword)
	if err != nil {
		return valkeyConnection{}, utils.AivenFail("ReadServiceUserSecret", application, err, false, logger)
	}
	host, err := operator.Required(serviceUser.Secret, operator.ServiceUserHost)
	if err != nil {
		return valkeyConnection{}, utils.AivenFail("ReadServiceUserSecret", application, err, false, logger)
	}
	port, err := operator.Required(serviceUser.Secret, operator.ServiceUserPort)
	if err != nil {
		return valkeyConnection{}, utils.AivenFail("ReadServiceUserSecret", application, err, false, logger)
	}
	return valkeyConnection{
		username:       serviceUser.Username,
		password:       password,
		uri:            fmt.Sprintf("valkeys://%s:%s", host, port),
		host:           host,
		port:           port,
		legacyUsername: res.Legacy,
	}, nil
}

// resolveServiceUserName reads this instance's annotations off the app secret
// and resolves them to a usable CR name.
func (h ValkeyHandler) resolveServiceUserName(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, valkeySpec *aiven_nais_io_v1.ValkeySpec, serviceName string, logger log.FieldLogger) (operator.NameResolution, error) {
	var existingName, existingLegacy string
	existing := &corev1.Secret{}
	if err := h.k8sReader.Get(ctx, client.ObjectKey{Namespace: application.GetNamespace(), Name: valkeySpec.SecretName}, existing); err != nil {
		if !k8serrors.IsNotFound(err) {
			return operator.NameResolution{}, err
		}
	} else {
		annotations := existing.GetAnnotations()
		existingName = annotations[instanceAnnotation(valkeySpec.Instance, ServiceUserAnnotation)]
		existingLegacy = annotations[instanceAnnotation(valkeySpec.Instance, LegacyServiceUserAnnotation)]
	}

	return operator.ResolveServiceUserName(ctx, h.crServiceUser, application.GetNamespace(), application.GetName(), valkeySpec.Access, valkeySpec.Instance, valkeySpec.SecretName, serviceName, existingName, existingLegacy, logger)
}

func instanceAnnotation(instance, annotation string) string {
	return fmt.Sprintf("%s.%s", keyName(instance, "-"), annotation)
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

func (h ValkeyHandler) Cleanup(ctx context.Context, secret *corev1.Secret, logger log.FieldLogger) error {
	annotations := secret.GetAnnotations()
	projectName, okProjectName := annotations[ProjectAnnotation]

	logger = logger.WithFields(log.Fields{
		"aivenProject": projectName,
		"secretName":   secret.Name,
	})
	for annotationKey := range annotations {
		// One serviceName annotation per instance; its prefix identifies the instance.
		if strings.HasSuffix(annotationKey, ServiceNameAnnotation) {
			serviceName := annotations[annotationKey]
			instance, _, _ := strings.Cut(annotationKey, ".")
			instanceLogger := logger.WithFields(log.Fields{
				"serviceName":          serviceName,
				"aivenServiceInstance": annotations[fmt.Sprintf("%s.%s", instance, InstanceAnnotation)],
			})

			serviceUserNameKey := fmt.Sprintf("%s.%s", instance, ServiceUserAnnotation)
			serviceUserName, okServiceUser := annotations[serviceUserNameKey]
			if !okServiceUser {
				instanceLogger.WithField(utils.FieldInvariant, "missing serviceUser annotation").Errorf("missing annotation %s", serviceUserNameKey)
				continue
			}
			instanceLogger = instanceLogger.WithField("serviceUser", serviceUserName)

			if !okProjectName {
				return fmt.Errorf("missing annotation %s", ProjectAnnotation)
			}

			if err := operator.DrainServiceUser(ctx, h.crServiceUser, h.serviceuser, secret.GetNamespace(), serviceUserName, serviceUserName, projectName, serviceName, instanceLogger); err != nil {
				return err
			}

			// A tracked legacy (pre-CR) user has no CR, so it is always deleted
			// directly via the Aiven API.
			if legacyUsername, ok := annotations[fmt.Sprintf("%s.%s", instance, LegacyServiceUserAnnotation)]; ok {
				if err := serviceuser.EnsureServiceUserDeleted(ctx, h.serviceuser, "legacy service user", legacyUsername, projectName, serviceName, instanceLogger); err != nil {
					return err
				}
			}
		}
	}

	return nil
}
