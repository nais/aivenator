package kafka

import (
	"fmt"
	"time"

	"github.com/aiven/aiven-go-client"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	"github.com/nais/liberator/pkg/namegen"
	"github.com/nais/liberator/pkg/strings"
	log "github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/nais/aivenator/constants"
	aivenator_aiven "github.com/nais/aivenator/pkg/aiven"
	"github.com/nais/aivenator/pkg/aiven/project"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/certificate"
	"github.com/nais/aivenator/pkg/utils"
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

func NewKafkaHandler(aiven *aiven.Client, projects []string) KafkaHandler {
	return KafkaHandler{
		project:     project.NewManager(aiven.CA),
		serviceuser: serviceuser.NewManager(aiven.ServiceUsers),
		service:     service.NewManager(aiven.Services),
		generator:   certificate.NewExecGenerator(),
		projects:    projects,
	}
}

type KafkaHandler struct {
	project     project.ProjectManager
	serviceuser serviceuser.ServiceUserManager
	service     service.ServiceManager
	generator   certificate.Generator
	projects    []string
}

func (h KafkaHandler) Apply(application *aiven_nais_io_v1.AivenApplication, secret *v1.Secret, logger *log.Entry) error {
	logger = logger.WithFields(log.Fields{"handler": "kafka"})
	if application.Spec.Kafka == nil {
		return nil
	}

	projectName := application.Spec.Kafka.Pool
	if projectName == "" {
		logger.Debugf("No Kafka pool specified; noop")
		return nil
	}
	serviceName := DefaultKafkaService(application.Spec.Kafka.Pool)

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
		return utils.AivenFail("GetService", application, err, logger)
	}

	ca, err := h.project.GetCA(projectName)
	if err != nil {
		return utils.AivenFail("GetCA", application, err, logger)
	}

	serviceUserName := namegen.RandShortName(application.ServiceUserPrefix(), aivenator_aiven.MaxServiceUserNameLength)
	aivenUser, err := h.serviceuser.Create(serviceUserName, projectName, serviceName)
	if err != nil {
		return utils.AivenFail("CreateServiceUser", application, err, logger)
	}

	secret.SetAnnotations(utils.MergeStringMap(secret.GetAnnotations(), map[string]string{
		ServiceUserAnnotation: aivenUser.Username,
		PoolAnnotation:        application.Spec.Kafka.Pool,
	}))
	logger.Infof("Created serviceuser %s", aivenUser.Username)

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

func (h KafkaHandler) Cleanup(secret *v1.Secret, logger *log.Entry) error {
	annotations := secret.GetAnnotations()
	if serviceUserName, okServiceUser := annotations[ServiceUserAnnotation]; okServiceUser {
		if projectName, okPool := annotations[PoolAnnotation]; okPool {
			serviceName := DefaultKafkaService(projectName)
			logger = logger.WithFields(log.Fields{
				"pool":    projectName,
				"service": serviceName,
			})
			err := h.serviceuser.Delete(serviceUserName, projectName, serviceName)
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

func DefaultKafkaService(project string) string {
	return project + "-kafka"
}
