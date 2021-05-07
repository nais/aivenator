package kafka

import (
	"fmt"
	"github.com/aiven/aiven-go-client"
	aivenator_aiven "github.com/nais/aivenator/pkg/aiven"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/utils"
	kafka_nais_io_v1 "github.com/nais/liberator/pkg/apis/kafka.nais.io/v1"
	"github.com/nais/liberator/pkg/namegen"
	log "github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
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
	}
}

type KafkaHandler struct {
	serviceuser *serviceuser.Manager
	service     *service.Manager
}

func (h KafkaHandler) Apply(application *kafka_nais_io_v1.AivenApplication, secret *v1.Secret, logger *log.Entry) error {
	logger = logger.WithFields(log.Fields{"handler": "kafka"})
	projectName := application.Spec.Kafka.Pool
	if projectName == "" {
		logger.Debugf("No Kafka pool specified; noop")
		return nil
	}
	serviceName := aivenator_aiven.DefaultKafkaService(application.Spec.Kafka.Pool)

	aivenService, err := h.service.Get(projectName, serviceName)
	if err != nil {
		aivenFail("GetService", application, err, logger)
		return err
	}

	ca, err := h.service.GetCA(projectName)
	if err != nil {
		aivenFail("GetCA", application, err, logger)
		return err
	}

	serviceUserName := namegen.RandShortName(application.ServiceUserPrefix(), aivenator_aiven.MaxServiceUserNameLength)
	aivenUser, err := h.serviceuser.Create(serviceUserName, projectName, serviceName)
	if err != nil {
		aivenFail("Create", application, err, logger)
		return err
	}
	secret.ObjectMeta.Annotations[aivenator_aiven.ServiceUserAnnotation] = aivenUser.Username
	logger.Infof("Created serviceName user %s", aivenUser.Username)

	kafkaBrokerAddress := service.GetKafkaBrokerAddress(aivenService)
	kafkaSchemaRegistryAddress := service.GetSchemaRegistryAddress(aivenService)

	utils.MergeInto(map[string]string{
		KafkaCertificate:       aivenUser.AccessCert,
		KafkaPrivateKey:        aivenUser.AccessKey,
		KafkaBrokers:           kafkaBrokerAddress,
		KafkaSchemaRegistry:    kafkaSchemaRegistryAddress,
		KafkaSchemaUser:        aivenUser.Username,
		KafkaSchemaPassword:    aivenUser.Password,
		KafkaCA:                ca,
		KafkaCredStorePassword: "", // TODO: credStore.Secret,
		KafkaSecretUpdated:     time.Now().Format(time.RFC3339),
	}, secret.StringData)

	// TODO: Add binary data to secret
	// TODO: Write some tests!

	return nil
}

func aivenFail(operation string, application *kafka_nais_io_v1.AivenApplication, err error, logger *log.Entry) {
	message := fmt.Errorf("operation %s failed in Aiven: %s", operation, err)
	logger.Error(message)
	application.Status.AddCondition(kafka_nais_io_v1.AivenApplicationCondition{
		Type:    kafka_nais_io_v1.AivenApplicationAivenFailure,
		Status:  v1.ConditionTrue,
		Reason:  operation,
		Message: message.Error(),
	})
}
