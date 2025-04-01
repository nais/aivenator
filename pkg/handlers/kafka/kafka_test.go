package kafka

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/aiven/project"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/certificate"
	"github.com/nais/aivenator/pkg/utils"
	liberator_service "github.com/nais/liberator/pkg/aiven/service"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

const (
	serviceUserName = "service-user-name"
	credStoreSecret = "my-secret"
	serviceURI      = "http://example.com"
	ca              = "my-ca"
	pool            = "my-testing-pool"
	invalidPool     = "not-my-testing-pool"
)

const (
	ServicesGetAddresses = iota
	ServiceUsersCreate
	ServiceUsersGet
	ServiceUsersGetNotFound
	ProjectGetCA
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

	logger             log.FieldLogger
	mockProjects       *project.MockProjectManager
	mockServiceUsers   *serviceuser.MockServiceUserManager
	mockServices       *service.MockServiceManager
	mockGenerator      *certificate.MockGenerator
	mockNameResolver   *liberator_service.MockNameResolver
	kafkaHandler       KafkaHandler
	applicationBuilder aiven_nais_io_v1.AivenApplicationBuilder
	ctx                context.Context
	cancel             context.CancelFunc
}

func (suite *KafkaHandlerTestSuite) SetupSuite() {
	suite.logger = log.NewEntry(log.New())
}

func (suite *KafkaHandlerTestSuite) addDefaultMocks(enabled map[int]struct{}) {
	if _, ok := enabled[ServicesGetAddresses]; ok {
		suite.mockServices.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
			Return(&service.ServiceAddresses{
				ServiceURI: serviceURI,
				SchemaRegistry: service.ServiceAddress{
					URI:  "",
					Host: "",
					Port: 0,
				},
			}, nil)
	}
	if _, ok := enabled[ProjectGetCA]; ok {
		suite.mockProjects.On("GetCA", mock.Anything, mock.Anything).
			Return(ca, nil)
	}
	if _, ok := enabled[ServiceUsersCreate]; ok {
		suite.mockServiceUsers.On("Create", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&aiven.ServiceUser{
				Username: serviceUserName,
			}, nil)
	}
	if _, ok := enabled[ServiceUsersGet]; ok {
		suite.mockServiceUsers.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&aiven.ServiceUser{
				Username: serviceUserName,
			}, nil)
	}
	if _, ok := enabled[ServiceUsersGetNotFound]; ok {
		suite.mockServiceUsers.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, aiven.Error{
				Message:  "aiven-error",
				MoreInfo: "aiven-more-info",
				Status:   404,
			})
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
	suite.mockServiceUsers = &serviceuser.MockServiceUserManager{}
	suite.mockServices = &service.MockServiceManager{}
	suite.mockProjects = &project.MockProjectManager{}
	suite.mockGenerator = &certificate.MockGenerator{}
	suite.mockNameResolver = liberator_service.NewMockNameResolver(suite.T())
	suite.mockNameResolver.On("ResolveKafkaServiceName", mock.Anything, "my-testing-pool").Maybe().Return("kafka", nil)
	suite.kafkaHandler = KafkaHandler{
		project:      suite.mockProjects,
		serviceuser:  suite.mockServiceUsers,
		service:      suite.mockServices,
		generator:    suite.mockGenerator,
		nameResolver: suite.mockNameResolver,
		projects:     []string{"dev-nais-dev", "my-testing-pool"},
	}
	suite.applicationBuilder = aiven_nais_io_v1.NewAivenApplicationBuilder("test-app", "test-ns")
	suite.ctx, suite.cancel = context.WithTimeout(context.Background(), 5*time.Second)
}

func (suite *KafkaHandlerTestSuite) TearDownTest() {
	suite.cancel()
}

func (suite *KafkaHandlerTestSuite) TestCleanupNoKafka() {
	secret := &v1.Secret{}
	err := suite.kafkaHandler.Cleanup(suite.ctx, secret, suite.logger)

	suite.NoError(err)
}

func (suite *KafkaHandlerTestSuite) TestCleanupServiceUser() {
	secret := &v1.Secret{}
	secret.SetAnnotations(map[string]string{
		ServiceUserAnnotation: serviceUserName,
		PoolAnnotation:        pool,
	})
	suite.mockServiceUsers.On("Delete", mock.Anything, serviceUserName, pool, mock.Anything, mock.Anything).
		Return(nil)

	err := suite.kafkaHandler.Cleanup(suite.ctx, secret, suite.logger)

	suite.NoError(err)
	suite.mockServiceUsers.AssertCalled(suite.T(), "Delete", mock.Anything, serviceUserName, pool, mock.Anything, mock.Anything)
}

func (suite *KafkaHandlerTestSuite) TestCleanupServiceUserAlreadyGone() {
	secret := &v1.Secret{}
	secret.SetAnnotations(map[string]string{
		ServiceUserAnnotation: serviceUserName,
		PoolAnnotation:        pool,
	})
	suite.mockServiceUsers.On("Delete", mock.Anything, serviceUserName, pool, mock.Anything, mock.Anything).
		Return(aiven.Error{
			Message: "Not Found",
			Status:  404,
		})

	err := suite.kafkaHandler.Cleanup(suite.ctx, secret, suite.logger)

	suite.NoError(err)
	suite.mockServiceUsers.AssertCalled(suite.T(), "Delete", mock.Anything, serviceUserName, pool, mock.Anything, mock.Anything)
}

func (suite *KafkaHandlerTestSuite) TestNoKafka() {
	application := suite.applicationBuilder.Build()
	secrets, err := suite.kafkaHandler.Apply(suite.ctx, &application, suite.logger)

	suite.NoError(err)
	suite.Equal(0, len(secrets))
}

func (suite *KafkaHandlerTestSuite) TestKafkaOk() {
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ProjectGetCA, ServiceUsersCreate, GeneratorMakeCredStores, ServiceUsersGetNotFound))
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Kafka: &aiven_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()
	secret := &v1.Secret{}
	_, err := suite.kafkaHandler.Apply(suite.ctx, &application, suite.logger)

	suite.NoError(err)
	expected := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				ServiceUserAnnotation: serviceUserName,
				PoolAnnotation:        pool,
			},
			Finalizers: []string{constants.AivenatorFinalizer},
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
	suite.ElementsMatch(keysFromByteMap(secret.Data), []string{KafkaKeystore, KafkaTruststore})
}

func (suite *KafkaHandlerTestSuite) TestSecretExists() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Kafka: &aiven_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				ServiceUserAnnotation: serviceUserName,
			},
		},
	}
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ProjectGetCA, GeneratorMakeCredStores, ServiceUsersGet))

	_, err := suite.kafkaHandler.Apply(suite.ctx, &application, suite.logger)

	suite.NoError(err)
	suite.Empty(validation.ValidateAnnotations(secret.GetAnnotations(), field.NewPath("metadata.annotations")))
	suite.Equal(secret.GetAnnotations()[ServiceUserAnnotation], serviceUserName)
}

func (suite *KafkaHandlerTestSuite) TestServiceGetFailed() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Kafka: &aiven_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()

	suite.addDefaultMocks(enabled(ProjectGetCA, ServiceUsersCreate, GeneratorMakeCredStores))
	suite.mockServices.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, aiven.Error{
			Message:  "aiven-error",
			MoreInfo: "aiven-more-info",
			Status:   500,
		})

	_, err := suite.kafkaHandler.Apply(suite.ctx, &application, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure))
}

func (suite *KafkaHandlerTestSuite) TestProjectGetCAFailed() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Kafka: &aiven_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()

	suite.addDefaultMocks(enabled(ServicesGetAddresses, ServiceUsersCreate, GeneratorMakeCredStores))
	suite.mockProjects.On("GetCA", mock.Anything, mock.Anything).
		Return("", aiven.Error{
			Message:  "aiven-error",
			MoreInfo: "aiven-more-info",
			Status:   500,
		})

	_, err := suite.kafkaHandler.Apply(suite.ctx, &application, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure))
}

func (suite *KafkaHandlerTestSuite) TestServiceUsersCreateFailed() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Kafka: &aiven_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()

	suite.addDefaultMocks(enabled(ServicesGetAddresses, ProjectGetCA, GeneratorMakeCredStores, ServiceUsersGetNotFound))
	suite.mockServiceUsers.On("Create", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, aiven.Error{
			Message:  "aiven-error",
			MoreInfo: "aiven-more-info",
			Status:   500,
		})

	_, err := suite.kafkaHandler.Apply(suite.ctx, &application, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure))
}

func (suite *KafkaHandlerTestSuite) TestServiceUserNotFound() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Kafka: &aiven_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				ServiceUserAnnotation: serviceUserName,
			},
		},
	}
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ProjectGetCA, ServiceUsersCreate, GeneratorMakeCredStores, ServiceUsersGetNotFound))

	_, err := suite.kafkaHandler.Apply(suite.ctx, &application, suite.logger)

	suite.NoError(err)
	expected := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				ServiceUserAnnotation: serviceUserName,
				PoolAnnotation:        pool,
			},
			Finalizers: []string{constants.AivenatorFinalizer},
		},
		Data:       secret.Data,
		StringData: secret.StringData,
	}
	suite.Equal(expected, secret)
}

func (suite *KafkaHandlerTestSuite) TestServiceUserCollision() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Kafka: &aiven_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{},
		},
	}
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ProjectGetCA, GeneratorMakeCredStores))
	suite.mockServiceUsers.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&aiven.ServiceUser{
			Username: serviceUserName,
		}, nil)

	_, err := suite.kafkaHandler.Apply(suite.ctx, &application, suite.logger)

	suite.mockServiceUsers.AssertNotCalled(suite.T(), "Create", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	suite.NoError(err)
	expected := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				ServiceUserAnnotation: serviceUserName,
				PoolAnnotation:        pool,
			},
			Finalizers: []string{constants.AivenatorFinalizer},
		},
		Data:       secret.Data,
		StringData: secret.StringData,
	}
	suite.Equal(expected, secret)
}

func (suite *KafkaHandlerTestSuite) TestInvalidPool() {
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ProjectGetCA, ServiceUsersCreate, GeneratorMakeCredStores))
	suite.mockNameResolver.On("ResolveKafkaServiceName", mock.Anything, "not-my-testing-pool").Maybe().Return("", utils.ErrUnrecoverable)

	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Kafka: &aiven_nais_io_v1.KafkaSpec{
				Pool: invalidPool,
			},
		}).
		Build()

	_, err := suite.kafkaHandler.Apply(suite.ctx, &application, suite.logger)

	suite.Error(err)
	suite.True(errors.Is(err, utils.ErrUnrecoverable))
}

func (suite *KafkaHandlerTestSuite) TestGeneratorMakeCredStoresFailed() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Kafka: &aiven_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()

	suite.addDefaultMocks(enabled(ServicesGetAddresses, ProjectGetCA, ServiceUsersCreate, ServiceUsersGetNotFound))
	suite.mockGenerator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("local-fail"))

	_, err := suite.kafkaHandler.Apply(suite.ctx, &application, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationLocalFailure))
}

func TestKafkaHandler(t *testing.T) {
	kafkaTestSuite := new(KafkaHandlerTestSuite)
	suite.Run(t, kafkaTestSuite)
}

func keysFromByteMap(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
