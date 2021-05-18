package kafka

import (
	"fmt"
	"github.com/aiven/aiven-go-client"
	aivenator_aiven "github.com/nais/aivenator/pkg/aiven"
	"github.com/nais/aivenator/pkg/certificate"
	"github.com/nais/aivenator/pkg/mocks"
	"github.com/nais/aivenator/pkg/utils"
	kafka_nais_io_v1 "github.com/nais/liberator/pkg/apis/kafka.nais.io/v1"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
)

const (
	serviceUserName = "service-user-name"
	credStoreSecret = "my-secret"
	serviceURI      = "http://example.com"
	ca              = "my-ca"
	pool            = "my-testing-pool"
)

const (
	ServicesGet = iota
	ServicesGetCA
	ServiceUsersCreate
	GeneratorMakeCredStores
)

func enabled(elements ...int) map[int]struct{} {
	m := make(map[int]struct{}, len(elements))
	for _, element := range elements {
		m[element] = struct{}{}
	}
	return m
}

type KafkaHandlerTestSuite struct {
	suite.Suite

	logger             *log.Entry
	mockServiceUsers   *mocks.ServiceUserManager
	mockServices       *mocks.ServiceManager
	mockGenerator      *mocks.Generator
	kafkaHandler       KafkaHandler
	applicationBuilder kafka_nais_io_v1.AivenApplicationBuilder
}

func (suite *KafkaHandlerTestSuite) SetupSuite() {
	suite.logger = log.NewEntry(log.New())
}

func (suite *KafkaHandlerTestSuite) addDefaultMocks(enabled map[int]struct{}) {
	if _, ok := enabled[ServicesGet]; ok {
		suite.mockServices.On("Get", mock.Anything, mock.Anything).
			Return(&aiven.Service{URI: serviceURI, Components: nil}, nil)
	}
	if _, ok := enabled[ServicesGetCA]; ok {
		suite.mockServices.On("GetCA", mock.Anything).
			Return(ca, nil)
	}
	if _, ok := enabled[ServiceUsersCreate]; ok {
		suite.mockServiceUsers.On("Create", mock.Anything, mock.Anything, mock.Anything).
			Return(&aiven.ServiceUser{
				Username: serviceUserName,
			}, nil)
	}
	if _, ok := enabled[GeneratorMakeCredStores]; ok {
		suite.mockGenerator.Mock.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
			Return(&certificate.CredStoreData{
				Keystore:   []byte("my-keystore"),
				Truststore: []byte("my-truststore"),
				Secret:     credStoreSecret,
			}, nil)
	}
}

func (suite *KafkaHandlerTestSuite) SetupTest() {
	suite.mockServiceUsers = &mocks.ServiceUserManager{}
	suite.mockServices = &mocks.ServiceManager{}
	suite.mockGenerator = &mocks.Generator{}
	suite.kafkaHandler = KafkaHandler{
		serviceuser: suite.mockServiceUsers,
		service:     suite.mockServices,
		generator:   suite.mockGenerator,
	}
	suite.applicationBuilder = kafka_nais_io_v1.NewAivenApplicationBuilder("test-app", "test-ns")
}

func (suite *KafkaHandlerTestSuite) TestCleanupNoKafka() {
	secret := &v1.Secret{}
	err := suite.kafkaHandler.Cleanup(secret, suite.logger)

	suite.NoError(err)
}

func (suite *KafkaHandlerTestSuite) TestCleanupServiceUser() {
	secret := &v1.Secret{}
	secret.SetAnnotations(map[string]string{
		aivenator_aiven.ServiceUserAnnotation: serviceUserName,
		aivenator_aiven.PoolAnnotation:        pool,
	})
	suite.mockServiceUsers.On("Delete", serviceUserName, pool, mock.Anything).
		Return(nil)

	err := suite.kafkaHandler.Cleanup(secret, suite.logger)

	suite.NoError(err)
	suite.mockServiceUsers.AssertCalled(suite.T(), "Delete", serviceUserName, pool, mock.Anything)
}

func (suite *KafkaHandlerTestSuite) TestCleanupServiceUserAlreadyGone() {
	secret := &v1.Secret{}
	secret.SetAnnotations(map[string]string{
		aivenator_aiven.ServiceUserAnnotation: serviceUserName,
		aivenator_aiven.PoolAnnotation:        pool,
	})
	suite.mockServiceUsers.On("Delete", serviceUserName, pool, mock.Anything).
		Return(aiven.Error{
			Message: "Not Found",
			Status:  404,
		})

	err := suite.kafkaHandler.Cleanup(secret, suite.logger)

	suite.NoError(err)
	suite.mockServiceUsers.AssertCalled(suite.T(), "Delete", serviceUserName, pool, mock.Anything)
}

func (suite *KafkaHandlerTestSuite) TestNoKafka() {
	application := suite.applicationBuilder.Build()
	secret := &v1.Secret{}
	err := suite.kafkaHandler.Apply(&application, secret, suite.logger)

	suite.NoError(err)
	suite.Equal(&v1.Secret{}, secret)
}

func (suite *KafkaHandlerTestSuite) TestKafkaOk() {
	suite.addDefaultMocks(enabled(ServicesGet, ServicesGetCA, ServiceUsersCreate, GeneratorMakeCredStores))
	application := suite.applicationBuilder.
		WithSpec(kafka_nais_io_v1.AivenApplicationSpec{
			Kafka: kafka_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()
	secret := &v1.Secret{}
	err := suite.kafkaHandler.Apply(&application, secret, suite.logger)

	suite.NoError(err)
	expected := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				aivenator_aiven.ServiceUserAnnotation: serviceUserName,
				aivenator_aiven.PoolAnnotation:        pool,
			},
			Finalizers: []string{kafka_nais_io_v1.AivenFinalizer},
		},
		// Check these individually
		Data:       secret.Data,
		StringData: secret.StringData,
	}
	suite.Equal(expected, secret)

	suite.ElementsMatch(utils.KeysFromStringMap(secret.StringData), []string{
		KafkaCA, KafkaPrivateKey, KafkaCredStorePassword, KafkaSchemaRegistry, KafkaSchemaUser, KafkaSchemaPassword,
		KafkaBrokers, KafkaSecretUpdated, KafkaCertificate,
	})
	suite.ElementsMatch(utils.KeysFromByteMap(secret.Data), []string{KafkaKeystore, KafkaTruststore})
}

func (suite *KafkaHandlerTestSuite) TestServiceGetFailed() {
	application := suite.applicationBuilder.
		WithSpec(kafka_nais_io_v1.AivenApplicationSpec{
			Kafka: kafka_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()
	secret := &v1.Secret{}
	suite.addDefaultMocks(enabled(ServicesGetCA, ServiceUsersCreate, GeneratorMakeCredStores))
	suite.mockServices.On("Get", mock.Anything, mock.Anything).
		Return(nil, &aiven.Error{
			Message:  "aiven-error",
			MoreInfo: "aiven-more-info",
			Status:   500,
		})

	err := suite.kafkaHandler.Apply(&application, secret, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(kafka_nais_io_v1.AivenApplicationAivenFailure))
}

func (suite *KafkaHandlerTestSuite) TestServiceGetCAFailed() {
	application := suite.applicationBuilder.
		WithSpec(kafka_nais_io_v1.AivenApplicationSpec{
			Kafka: kafka_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()
	secret := &v1.Secret{}
	suite.addDefaultMocks(enabled(ServicesGet, ServiceUsersCreate, GeneratorMakeCredStores))
	suite.mockServices.On("GetCA", mock.Anything).
		Return("", &aiven.Error{
			Message:  "aiven-error",
			MoreInfo: "aiven-more-info",
			Status:   500,
		})

	err := suite.kafkaHandler.Apply(&application, secret, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(kafka_nais_io_v1.AivenApplicationAivenFailure))
}

func (suite *KafkaHandlerTestSuite) TestServiceUsersCreateFailed() {
	application := suite.applicationBuilder.
		WithSpec(kafka_nais_io_v1.AivenApplicationSpec{
			Kafka: kafka_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()
	secret := &v1.Secret{}
	suite.addDefaultMocks(enabled(ServicesGet, ServicesGetCA, GeneratorMakeCredStores))
	suite.mockServiceUsers.On("Create", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, &aiven.Error{
			Message:  "aiven-error",
			MoreInfo: "aiven-more-info",
			Status:   500,
		})

	err := suite.kafkaHandler.Apply(&application, secret, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(kafka_nais_io_v1.AivenApplicationAivenFailure))
}

func (suite *KafkaHandlerTestSuite) TestGeneratorMakeCredStoresFailed() {
	application := suite.applicationBuilder.
		WithSpec(kafka_nais_io_v1.AivenApplicationSpec{
			Kafka: kafka_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()
	secret := &v1.Secret{}
	suite.addDefaultMocks(enabled(ServicesGet, ServicesGetCA, ServiceUsersCreate))
	suite.mockGenerator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("local-fail"))

	err := suite.kafkaHandler.Apply(&application, secret, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(kafka_nais_io_v1.AivenApplicationLocalFailure))
}

func TestKafkaHandler(t *testing.T) {
	kafkaTestSuite := new(KafkaHandlerTestSuite)
	suite.Run(t, kafkaTestSuite)
}
