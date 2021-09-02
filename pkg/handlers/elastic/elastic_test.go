package elastic

import (
	"github.com/nais/aivenator/pkg/utils"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"

	"github.com/aiven/aiven-go-client"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/mocks"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

const (
	namespace       = "team-a"
	serviceUserName = "team-a"
	servicePassword = "service-password"
	projectName     = "my-project"
	serviceURI      = "http://example.com"
	instance        = "my-instance"
	access          = "read"
)

const (
	ServicesGetAddresses = iota
	ServiceUsersGet
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
	mockProjects       *mocks.ProjectManager
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
		project:     suite.mockProjects,
		serviceuser: suite.mockServiceUsers,
		service:     suite.mockServices,
		projectName: projectName,
	}
	suite.applicationBuilder = aiven_nais_io_v1.NewAivenApplicationBuilder("test-app", namespace)
}

func (suite *ElasticHandlerTestSuite) TestNoElastic() {
	suite.addDefaultMocks(enabled(ServicesGetAddresses))
	application := suite.applicationBuilder.Build()
	secret := &v1.Secret{}
	err := suite.elasticHandler.Apply(&application, secret, suite.logger)

	suite.NoError(err)
	suite.Equal(&v1.Secret{}, secret)
}

func (suite *ElasticHandlerTestSuite) TestElasticOk() {
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ServiceUsersGet))
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Elastic: &aiven_nais_io_v1.ElasticSpec{
				Instance: instance,
				Access:   access,
			},
		}).
		Build()
	secret := &v1.Secret{}
	err := suite.elasticHandler.Apply(&application, secret, suite.logger)

	suite.NoError(err)
	expected := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				ProjectAnnotation:     projectName,
				ServiceUserAnnotation: serviceUserName,
			},
		},
		// Check these individually
		Data:       secret.Data,
		StringData: secret.StringData,
	}
	suite.Equal(expected, secret)
	suite.ElementsMatch(utils.KeysFromStringMap(secret.StringData), []string{
		ElasticUser, ElasticPassword, ElasticURI,
	})
}

func (suite *ElasticHandlerTestSuite) TestServiceGetFailed() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Elastic: &aiven_nais_io_v1.ElasticSpec{
				Instance: instance,
				Access:   access,
			},
		}).
		Build()
	secret := &v1.Secret{}
	suite.addDefaultMocks(enabled(ServiceUsersGet))
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

func (suite *ElasticHandlerTestSuite) TestServiceUsersGetFailed() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Elastic: &aiven_nais_io_v1.ElasticSpec{
				Instance: instance,
				Access:   access,
			},
		}).
		Build()
	secret := &v1.Secret{}
	suite.addDefaultMocks(enabled(ServicesGetAddresses))
	suite.mockServiceUsers.On("Get", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, &aiven.Error{
			Message:  "aiven-error",
			MoreInfo: "aiven-more-info",
			Status:   500,
		})

	err := suite.elasticHandler.Apply(&application, secret, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure))
}

func (suite *ElasticHandlerTestSuite) TestCorrectServiceUserSelected() {
	testData := []struct {
		access   string
		username string
	}{
		{
			access:   "read",
			username: serviceUserName + "-r",
		},
		{
			access:   "readwrite",
			username: serviceUserName + "-rw",
		},
		{
			access:   "write",
			username: serviceUserName + "-w",
		},
		{
			access:   "admin",
			username: serviceUserName,
		},
	}

	for _, t := range testData {
		suite.Run(t.access, func() {
			suite.addDefaultMocks(enabled(ServicesGetAddresses))
			suite.mockServiceUsers.On("Get", mock.Anything, mock.Anything, mock.Anything).
				Return(&aiven.ServiceUser{
					Username: t.username,
					Password: servicePassword,
				}, nil).Once()
			application := suite.applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Elastic: &aiven_nais_io_v1.ElasticSpec{
						Instance: instance,
						Access:   t.access,
					},
				}).
				Build()
			secret := &v1.Secret{}
			err := suite.elasticHandler.Apply(&application, secret, suite.logger)

			suite.NoError(err)
			suite.Equal(t.username, secret.StringData[ElasticUser])
		})
	}
}

func TestElasticHandler(t *testing.T) {
	elasticTestSuite := new(ElasticHandlerTestSuite)
	suite.Run(t, elasticTestSuite)
}
