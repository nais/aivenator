package kafka

import (
	"fmt"
	"github.com/aiven/aiven-go-client"
	aivenator_aiven "github.com/nais/aivenator/pkg/aiven"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/certificate"
	"github.com/nais/aivenator/pkg/utils"
	kafka_nais_io_v1 "github.com/nais/liberator/pkg/apis/kafka.nais.io/v1"
	"github.com/nais/liberator/pkg/namegen"
	log "github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"time"
)

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

func NewKafkaHandler(aiven *aiven.Client) KafkaHandler {
	return KafkaHandler{
		serviceuser: serviceuser.NewManager(aiven.ServiceUsers),
		service:     service.NewManager(aiven.Services, aiven.CA),
		generator:   certificate.NewExecGenerator(),
	}
}

type KafkaHandler struct {
	serviceuser serviceuser.ServiceUserManager
	service     service.ServiceManager
	generator   certificate.Generator
}

func (h KafkaHandler) Apply(application *kafka_nais_io_v1.AivenApplication, secret *v1.Secret, logger *log.Entry) error {
	logger = logger.WithFields(log.Fields{"handler": "kafka"})
	projectName := application.Spec.Kafka.Pool
	if projectName == "" {
		logger.Debugf("No Kafka pool specified; noop")
		return nil
	}
	serviceName := aivenator_aiven.DefaultKafkaService(application.Spec.Kafka.Pool)

	logger = logger.WithFields(log.Fields{
		"pool":    projectName,
		"service": serviceName,
	})

	aivenService, err := h.service.Get(projectName, serviceName)
	if err != nil {
		return utils.AivenFail("GetService", application, err, logger)
	}
	kafkaBrokerAddress := service.GetKafkaBrokerAddress(aivenService)
	kafkaSchemaRegistryAddress := service.GetSchemaRegistryAddress(aivenService)

	ca, err := h.service.GetCA(projectName)
	if err != nil {
		return utils.AivenFail("GetCA", application, err, logger)
	}

	serviceUserName := namegen.RandShortName(application.ServiceUserPrefix(), aivenator_aiven.MaxServiceUserNameLength)
	aivenUser, err := h.serviceuser.Create(serviceUserName, projectName, serviceName)
	if err != nil {
		return utils.AivenFail("CreateServiceUser", application, err, logger)
	}

	secret.SetAnnotations(utils.MergeStringMap(secret.GetAnnotations(), map[string]string{
		aivenator_aiven.ServiceUserAnnotation: aivenUser.Username,
		aivenator_aiven.PoolAnnotation:        application.Spec.Kafka.Pool,
	}))
	logger.Infof("Created serviceName user %s", aivenUser.Username)

	credStore, err := h.generator.MakeCredStores(aivenUser.AccessKey, aivenUser.AccessCert, ca)
	if err != nil {
		utils.LocalFail("CreateCredStores", application, err, logger)
		return err
	}

	secret.StringData = utils.MergeStringMap(secret.StringData, map[string]string{
		KafkaCertificate:       aivenUser.AccessCert,
		KafkaPrivateKey:        aivenUser.AccessKey,
		KafkaBrokers:           kafkaBrokerAddress,
		KafkaSchemaRegistry:    kafkaSchemaRegistryAddress,
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

	controllerutil.AddFinalizer(secret, kafka_nais_io_v1.AivenFinalizer)

	return nil
}

func (h KafkaHandler) Cleanup(secret *v1.Secret, logger *log.Entry) error {
	annotations := secret.GetAnnotations()
	if serviceUserName, okServiceUser := annotations[aivenator_aiven.ServiceUserAnnotation]; okServiceUser {
		if projectName, okPool := annotations[aivenator_aiven.PoolAnnotation]; okPool {
			serviceName := aivenator_aiven.DefaultKafkaService(projectName)
			err := h.serviceuser.Delete(serviceUserName, projectName, serviceName)
			if err != nil {
				return err
			}
			logger.WithFields(log.Fields{
				"pool":    projectName,
				"service": serviceName,
			}).Infof("Deleted service user %s", serviceUserName)
		} else {
			return fmt.Errorf("missing pool annotation on secret %s in namespace %s, unable to delete service user %s",
				secret.GetName(), secret.GetNamespace(), serviceUserName)
		}

	}
	return nil
}
