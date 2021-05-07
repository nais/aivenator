package kafka

import (
	"fmt"
	"github.com/aiven/aiven-go-client"
	aivenator_aiven "github.com/nais/aivenator/pkg/aiven"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/metrics"
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
		aiven: aivenator_aiven.Interfaces{
			CA:           aiven.CA,
			Service:      aiven.Services,
			ServiceUsers: aiven.ServiceUsers,
		},
	}
}

type KafkaHandler struct {
	aiven aivenator_aiven.Interfaces
}

func (h KafkaHandler) Apply(application *kafka_nais_io_v1.AivenApplication, secret *v1.Secret, logger *log.Entry) error {
	logger = logger.WithFields(log.Fields{"handler": "kafka"})
	projectName := application.Spec.Kafka.Pool
	if projectName == "" {
		logger.Debugf("No Kafka pool specified; noop")
		return nil
	}
	serviceName := aivenator_aiven.DefaultKafkaService(application.Spec.Kafka.Pool)

	aivenService, err := h.getService(application, projectName, serviceName, logger)
	if err != nil {
		return err
	}

	ca, err := h.getCA(application, projectName, logger)
	if err != nil {
		return err
	}

	aivenUser, err := h.createServiceUser(application, projectName, serviceName, logger)
	if err != nil {
		return err
	}
	secret.ObjectMeta.Annotations[aivenator_aiven.ServiceUserAnnotation] = aivenUser.Username

	kafkaBrokerAddress := service.GetKafkaBrokerAddress(aivenService)
	kafkaSchemaRegistryAddress := service.GetSchemaRegistryAddress(aivenService)

	stringData := map[string]string{
		KafkaCertificate:       aivenUser.AccessCert,
		KafkaPrivateKey:        aivenUser.AccessKey,
		KafkaBrokers:           kafkaBrokerAddress,
		KafkaSchemaRegistry:    kafkaSchemaRegistryAddress,
		KafkaSchemaUser:        aivenUser.Username,
		KafkaSchemaPassword:    aivenUser.Password,
		KafkaCA:                ca,
		KafkaCredStorePassword: "", // TODO: credStore.Secret,
		KafkaSecretUpdated:     time.Now().Format(time.RFC3339),
	}

	// TODO: Merge stringData into secret
	// TODO: Add binary data to secret
	// TODO: Write some tests!
	logger.Debug(stringData)

	return nil
}

func (h KafkaHandler) getCA(application *kafka_nais_io_v1.AivenApplication, projectName string, logger *log.Entry) (string, error) {
	var ca string
	err := metrics.ObserveAivenLatency("CA_Get", projectName, func() error {
		var err error
		ca, err = h.aiven.CA.Get(projectName)
		return err
	})
	if err != nil {
		message := fmt.Errorf("unable to get CA from Aiven: %s", err)
		logger.Error(message)
		application.Status.AddCondition(kafka_nais_io_v1.AivenApplicationCondition{
			Type:    kafka_nais_io_v1.AivenApplicationAivenFailure,
			Status:  v1.ConditionTrue,
			Reason:  "CA_Get",
			Message: message.Error(),
		})
		return "", err
	}
	return ca, nil
}

func (h KafkaHandler) getService(application *kafka_nais_io_v1.AivenApplication, projectName, serviceName string, logger *log.Entry) (*aiven.Service, error) {
	var aivenService *aiven.Service
	err := metrics.ObserveAivenLatency("Service_Get", projectName, func() error {
		var err error
		aivenService, err = h.aiven.Service.Get(projectName, serviceName)
		return err
	})
	if err != nil {
		message := fmt.Errorf("unable to get serviceName information from Aiven: %s", err)
		logger.Error(message)
		application.Status.AddCondition(kafka_nais_io_v1.AivenApplicationCondition{
			Type:    kafka_nais_io_v1.AivenApplicationAivenFailure,
			Status:  v1.ConditionTrue,
			Reason:  "Service_Get",
			Message: message.Error(),
		})
		return nil, err
	}
	return aivenService, nil
}

func (h KafkaHandler) createServiceUser(application *kafka_nais_io_v1.AivenApplication, projectName, serviceName string, logger *log.Entry) (*aiven.ServiceUser, error) {
	serviceUserName := namegen.RandShortName(application.ServiceUserPrefix(), aivenator_aiven.MaxServiceUserNameLength)

	req := aiven.CreateServiceUserRequest{
		Username: serviceUserName,
	}

	var aivenUser *aiven.ServiceUser
	err := metrics.ObserveAivenLatency("ServiceUser_Create", projectName, func() error {
		var err error
		aivenUser, err = h.aiven.ServiceUsers.Create(projectName, serviceName, req)
		return err
	})
	if err != nil {
		message := fmt.Errorf("unable to create serviceName user in Aiven: %s", err)
		logger.Error(message)
		application.Status.AddCondition(kafka_nais_io_v1.AivenApplicationCondition{
			Type:    kafka_nais_io_v1.AivenApplicationAivenFailure,
			Status:  v1.ConditionTrue,
			Reason:  "ServiceUser_Create",
			Message: message.Error(),
		})
		return nil, err
	}
	logger.Infof("Created serviceName user %s", aivenUser.Username)
	return aivenUser, nil
}
