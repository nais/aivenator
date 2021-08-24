package elastic

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aiven/aiven-go-client"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/certificate"
	"github.com/nais/aivenator/pkg/mocks"
	"github.com/nais/aivenator/pkg/utils"
)

const (
	serviceUserName = "service-username"
	servicePassword = "service-password"
	project         = "my-project"
	serviceURI      = "http://example.com"
)

const (
	ServicesGetAddresses = iota
	ServiceUsersGet
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

type ElasticHandlerTestSuite struct {
	suite.Suite

	logger             *log.Entry
	mockServiceUsers   *mocks.ServiceUserManager
	mockServices       *mocks.ServiceManager
	elasticHandler     ElasticHandler
	applicationBuilder aiven_nais_io_v1.AivenApplicationBuilder
}

func (suite *ElasticHandlerTestSuite) SetupSuite() {
	suite.logger = log.NewEntry(log.New())
}

func (suite *ElasticHandlerTestSuite) addDefaultMocks(enabled map[int]struct{}) {
	if _, ok := enabled[ServicesGetAddresses]; ok {
		suite.mockServices.On("GetServiceAddresses", mock.Anything, mock.Anything).
			Return(&service.ServiceAddresses{
				ServiceURI:     serviceURI,
				SchemaRegistry: "",
			}, nil)
	}
	if _, ok := enabled[ServiceUsersGet]; ok {
		suite.mockServiceUsers.On("Get", mock.Anything, mock.Anything, mock.Anything).
			Return(&aiven.ServiceUser{
				Username: serviceUserName,
				Password: servicePassword,
			}, nil)
	}
}

func (suite *ElasticHandlerTestSuite) SetupTest() {
	suite.mockServiceUsers = &mocks.ServiceUserManager{}
	suite.mockServices = &mocks.ServiceManager{}
	suite.elasticHandler = ElasticHandler{
		project:     project,
		serviceuser: suite.mockServiceUsers,
		service:     suite.mockServices,
		generator:   suite.mockGenerator,
		projects:    []string{"nav-integration-test", "my-testing-pool"},
	}
	suite.applicationBuilder = aiven_nais_io_v1.NewAivenApplicationBuilder("test-app", "test-ns")
}

func (suite *ElasticHandlerTestSuite) TestCleanupNoElastic() {
	secret := &v1.Secret{}
	err := suite.elasticHandler.Cleanup(secret, suite.logger)

	suite.NoError(err)
}

func (suite *ElasticHandlerTestSuite) TestCleanupServiceUser() {
	secret := &v1.Secret{}
	secret.SetAnnotations(map[string]string{
		ServiceUserAnnotation: serviceUserName,
		PoolAnnotation:        pool,
	})
	suite.mockServiceUsers.On("Delete", serviceUserName, pool, mock.Anything).
		Return(nil)

	err := suite.elasticHandler.Cleanup(secret, suite.logger)

	suite.NoError(err)
	suite.mockServiceUsers.AssertCalled(suite.T(), "Delete", serviceUserName, pool, mock.Anything)
}

func (suite *ElasticHandlerTestSuite) TestCleanupServiceUserAlreadyGone() {
	secret := &v1.Secret{}
	secret.SetAnnotations(map[string]string{
		ServiceUserAnnotation: serviceUserName,
		PoolAnnotation:        pool,
	})
	suite.mockServiceUsers.On("Delete", serviceUserName, pool, mock.Anything).
		Return(aiven.Error{
			Message: "Not Found",
			Status:  404,
		})

	err := suite.elasticHandler.Cleanup(secret, suite.logger)

	suite.NoError(err)
	suite.mockServiceUsers.AssertCalled(suite.T(), "Delete", serviceUserName, pool, mock.Anything)
}

func (suite *ElasticHandlerTestSuite) TestNoElastic() {
	application := suite.applicationBuilder.Build()
	secret := &v1.Secret{}
	err := suite.elasticHandler.Apply(&application, secret, suite.logger)

	suite.NoError(err)
	suite.Equal(&v1.Secret{}, secret)
}

func (suite *ElasticHandlerTestSuite) TestElasticOk() {
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ProjectGetCA, ServiceUsersGet, GeneratorMakeCredStores))
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Elastic: aiven_nais_io_v1.ElasticSpec{
				Pool: pool,
			},
		}).
		Build()
	secret := &v1.Secret{}
	err := suite.elasticHandler.Apply(&application, secret, suite.logger)

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
		ElasticCA, ElasticPrivateKey, ElasticCredStorePassword, ElasticSchemaRegistry, ElasticSchemaUser, ElasticSchemaPassword,
		ElasticBrokers, ElasticSecretUpdated, ElasticCertificate,
	})
	suite.ElementsMatch(utils.KeysFromByteMap(secret.Data), []string{ElasticKeystore, ElasticTruststore})
}

func (suite *ElasticHandlerTestSuite) TestServiceGetFailed() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Elastic: aiven_nais_io_v1.ElasticSpec{
				Pool: pool,
			},
		}).
		Build()
	secret := &v1.Secret{}
	suite.addDefaultMocks(enabled(ProjectGetCA, ServiceUsersGet, GeneratorMakeCredStores))
	suite.mockServices.On("GetServiceAddresses", mock.Anything, mock.Anything).
		Return(nil, &aiven.Error{
			Message:  "aiven-error",
			MoreInfo: "aiven-more-info",
			Status:   500,
		})

	err := suite.elasticHandler.Apply(&application, secret, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure))
}

func (suite *ElasticHandlerTestSuite) TestProjectGetCAFailed() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Elastic: aiven_nais_io_v1.ElasticSpec{
				Pool: pool,
			},
		}).
		Build()
	secret := &v1.Secret{}
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ServiceUsersGet, GeneratorMakeCredStores))
	suite.mockProjects.On("GetCA", mock.Anything).
		Return("", &aiven.Error{
			Message:  "aiven-error",
			MoreInfo: "aiven-more-info",
			Status:   500,
		})

	err := suite.elasticHandler.Apply(&application, secret, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure))
}

func (suite *ElasticHandlerTestSuite) TestServiceUsersCreateFailed() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Elastic: aiven_nais_io_v1.ElasticSpec{
				Pool: pool,
			},
		}).
		Build()
	secret := &v1.Secret{}
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ProjectGetCA, GeneratorMakeCredStores))
	suite.mockServiceUsers.On("Create", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, &aiven.Error{
			Message:  "aiven-error",
			MoreInfo: "aiven-more-info",
			Status:   500,
		})

	err := suite.elasticHandler.Apply(&application, secret, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure))
}

func (suite *ElasticHandlerTestSuite) TestInvalidPool() {
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ProjectGetCA, ServiceUsersGet, GeneratorMakeCredStores))
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Elastic: aiven_nais_io_v1.ElasticSpec{
				Pool: invalidPool,
			},
		}).
		Build()
	secret := &v1.Secret{}
	err := suite.elasticHandler.Apply(&application, secret, suite.logger)

	suite.Error(err)
	suite.True(errors.Is(err, utils.UnrecoverableError))
	suite.NotNil(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationLocalFailure))
}

func (suite *ElasticHandlerTestSuite) TestGeneratorMakeCredStoresFailed() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Elastic: aiven_nais_io_v1.ElasticSpec{
				Pool: pool,
			},
		}).
		Build()
	secret := &v1.Secret{}
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ProjectGetCA, ServiceUsersGet))
	suite.mockGenerator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("local-fail"))

	err := suite.elasticHandler.Apply(&application, secret, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationLocalFailure))
}

func TestElasticHandler(t *testing.T) {
	elasticTestSuite := new(ElasticHandlerTestSuite)
	suite.Run(t, elasticTestSuite)
}
