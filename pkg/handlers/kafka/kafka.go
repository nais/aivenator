package kafka

import (
	"context"
	"encoding/base64"
	"fmt"
	"hash/crc32"
	"os"
	"time"

	"github.com/aiven/aiven-go-client"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	kafka_nais_io_v1 "github.com/nais/liberator/pkg/apis/kafka.nais.io/v1"
	"github.com/nais/liberator/pkg/strings"
	log "github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/aiven/project"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/certificate"
	"github.com/nais/aivenator/pkg/utils"
	liberator_service "github.com/nais/liberator/pkg/aiven/service"
)

// Keys in secret
const (
	KafkaBrokers           = "KAFKA_BROKERS"
	KafkaSchemaRegistry    = "KAFKA_SCHEMA_REGISTRY"
	KafkaSchemaUser        = "KAFKA_SCHEMA_REGISTRY_USER"
	KafkaSchemaPassword    = "KAFKA_SCHEMA_REGISTRY_PASSWORD"
	KafkaCertificate       = "KAFKA_CERTIFICATE"
	KafkaPrivateKey        = "KAFKA_PRIVATE_KEY"
	KafkaCA                = "KAFKA_CA"
	KafkaCredStorePassword = "KAFKA_CREDSTORE_PASSWORD"
	KafkaSecretUpdated     = "KAFKA_SECRET_UPDATED"
	KafkaKeystore          = "client.keystore.p12"
	KafkaTruststore        = "client.truststore.jks"
)

// Annotations
const (
	ServiceUserAnnotation = "kafka.aiven.nais.io/serviceUser"
	PoolAnnotation        = "kafka.aiven.nais.io/pool"
)

var clusterName = ""

func NewKafkaHandler(ctx context.Context, aiven *aiven.Client, projects []string, logger *log.Entry) KafkaHandler {
	generator := certificate.NewNativeGenerator()
	handler := KafkaHandler{
		project:      project.NewManager(aiven.CA),
		serviceuser:  serviceuser.NewManager(ctx, aiven.ServiceUsers),
		service:      service.NewManager(aiven.Services),
		generator:    generator,
		nameResolver: liberator_service.NewCachedNameResolver(aiven.Services),
		projects:     projects,
	}
	handler.StartUserCounter(ctx, logger)
	return handler
}

type KafkaHandler struct {
	project      project.ProjectManager
	serviceuser  serviceuser.ServiceUserManager
	service      service.ServiceManager
	generator    certificate.Generator
	nameResolver liberator_service.NameResolver
	projects     []string
}

func (h KafkaHandler) Apply(application *aiven_nais_io_v1.AivenApplication, secret *v1.Secret, logger log.FieldLogger) error {
	logger = logger.WithFields(log.Fields{"handler": "kafka"})
	if application.Spec.Kafka == nil {
		return nil
	}

	projectName := application.Spec.Kafka.Pool
	if projectName == "" {
		logger.Debugf("No Kafka pool specified; noop")
		return nil
	}

	serviceName, err := h.nameResolver.ResolveKafkaServiceName(application.Spec.Kafka.Pool)
	if err != nil {
		return utils.AivenFail("ResolveServiceName", application, err, false, logger)
	}

	logger = logger.WithFields(log.Fields{
		"pool":    projectName,
		"service": serviceName,
	})

	if !strings.ContainsString(h.projects, projectName) {
		err := fmt.Errorf("pool %s is not allowed in this cluster: %w", projectName, utils.UnrecoverableError)
		utils.LocalFail("ValidatePool", application, err, logger)
		return err
	}

	addresses, err := h.service.GetServiceAddresses(projectName, serviceName)
	if err != nil {
		return utils.AivenFail("GetService", application, err, false, logger)
	}

	ca, err := h.project.GetCA(projectName)
	if err != nil {
		return utils.AivenFail("GetCA", application, err, false, logger)
	}

	aivenUser, err := h.provideServiceUser(application, projectName, serviceName, secret, logger)
	if err != nil {
		return err
	}

	secret.SetAnnotations(utils.MergeStringMap(secret.GetAnnotations(), map[string]string{
		ServiceUserAnnotation: aivenUser.Username,
		PoolAnnotation:        application.Spec.Kafka.Pool,
	}))
	logger.Infof("Created service user %s", aivenUser.Username)

	credStore, err := h.generator.MakeCredStores(aivenUser.AccessKey, aivenUser.AccessCert, ca)
	if err != nil {
		utils.LocalFail("CreateCredStores", application, err, logger)
		return err
	}

	secret.StringData = utils.MergeStringMap(secret.StringData, map[string]string{
		KafkaCertificate:       aivenUser.AccessCert,
		KafkaPrivateKey:        aivenUser.AccessKey,
		KafkaBrokers:           addresses.ServiceURI,
		KafkaSchemaRegistry:    addresses.SchemaRegistry,
		KafkaSchemaUser:        aivenUser.Username,
		KafkaSchemaPassword:    aivenUser.Password,
		KafkaCA:                ca,
		KafkaCredStorePassword: credStore.Secret,
		KafkaSecretUpdated:     time.Now().Format(time.RFC3339),
	})

	secret.Data = utils.MergeByteMap(secret.Data, map[string][]byte{
		KafkaKeystore:   credStore.Keystore,
		KafkaTruststore: credStore.Truststore,
	})

	controllerutil.AddFinalizer(secret, constants.AivenatorFinalizer)

	return nil
}

func (h KafkaHandler) provideServiceUser(application *aiven_nais_io_v1.AivenApplication, projectName string, serviceName string, secret *v1.Secret, logger log.FieldLogger) (*aiven.ServiceUser, error) {
	var aivenUser *aiven.ServiceUser
	var err error

	suffix, err := createSuffix(application)
	if err != nil {
		err = fmt.Errorf("unable to create service user suffix: %s %w", err, utils.UnrecoverableError)
		utils.LocalFail("CreateSuffix", application, err, logger)
		return nil, err
	}

	var serviceUserName string

	if nameFromAnnotation, ok := secret.GetAnnotations()[ServiceUserAnnotation]; ok {
		serviceUserName = nameFromAnnotation
	} else {
		serviceUserName, err = kafka_nais_io_v1.ServiceUserNameWithSuffix(application.Namespace, application.Name, suffix)
		if err != nil {
			err = fmt.Errorf("unable to create service user name: %s %w", err, utils.UnrecoverableError)
			utils.LocalFail("ServiceUserNameWithSuffix", application, err, logger)
			return nil, err
		}
	}

	aivenUser, err = h.serviceuser.Get(serviceUserName, projectName, serviceName, logger)
	if err == nil {
		return aivenUser, nil
	}
	if !aiven.IsNotFound(err) {
		return nil, utils.AivenFail("GetServiceUser", application, err, false, logger)
	}

	aivenUser, err = h.serviceuser.Create(serviceUserName, projectName, serviceName, nil, logger)
	if err != nil {
		return nil, utils.AivenFail("CreateServiceUser", application, err, false, logger)
	}
	return aivenUser, nil
}

func createSuffix(application *aiven_nais_io_v1.AivenApplication) (string, error) {
	hasher := crc32.NewIEEE()
	basename := fmt.Sprintf("%d%s", application.Generation, clusterName)
	_, err := hasher.Write([]byte(basename))
	if err != nil {
		return "", err
	}
	bytes := make([]byte, 0, 4)
	suffix := base64.RawURLEncoding.EncodeToString(hasher.Sum(bytes))
	return suffix[:3], nil
}

func (h KafkaHandler) Cleanup(secret *v1.Secret, logger *log.Entry) error {
	annotations := secret.GetAnnotations()
	if serviceUserName, okServiceUser := annotations[ServiceUserAnnotation]; okServiceUser {
		if projectName, okPool := annotations[PoolAnnotation]; okPool {
			serviceName, err := h.nameResolver.ResolveKafkaServiceName(projectName)
			if err != nil {
				return err
			}
			logger = logger.WithFields(log.Fields{
				"pool":    projectName,
				"service": serviceName,
			})
			err = h.serviceuser.Delete(serviceUserName, projectName, serviceName, logger)
			if err != nil {
				if aiven.IsNotFound(err) {
					logger.Infof("Service user %s does not exist", serviceUserName)
					return nil
				}
				return err
			}
			logger.Infof("Deleted service user %s", serviceUserName)
		} else {
			return fmt.Errorf("missing pool annotation on secret %s in namespace %s, unable to delete service user %s",
				secret.GetName(), secret.GetNamespace(), serviceUserName)
		}
	}
	return nil
}

func (h *KafkaHandler) StartUserCounter(ctx context.Context, logger *log.Entry) {
	go h.countUsers(ctx, logger)
}

func (h *KafkaHandler) countUsers(ctx context.Context, logger *log.Entry) {
	ticker := time.NewTicker(h.serviceuser.GetCacheExpiration())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, prj := range h.projects {
				serviceName, err := h.nameResolver.ResolveKafkaServiceName(prj)
				if err != nil {
					logger.Warnf("unable to count service users for pool %s: %v", prj, err)
					continue
				}
				h.serviceuser.ObserveServiceUsersCount(prj, serviceName, logger)
			}
		}
	}
}

func init() {
	clusterName = os.Getenv("NAIS_CLUSTER_NAME")
}
