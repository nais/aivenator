package opensearch

import (
	"context"
	"testing"
	"time"

	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/aiven/opensearch"
	"github.com/nais/aivenator/pkg/aiven/project"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/handlers/secret"

	"github.com/nais/aivenator/pkg/utils"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/aiven/service"
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
	serviceURI      = "http://example.com:1234"
	serviceHost     = "example.com"
	servicePort     = 1234
	instance        = "my-instance"
	access          = "read"
)

const (
	ServicesGetAddresses = iota
	ServiceUsersGet
	ServiceUsersCreate
	OpenSearchACLGet
	OpenSearchACLUpdate
	ProjectCAGet
)

func enabled(elements ...int) map[int]struct{} {
	m := make(map[int]struct{}, len(elements))
	for _, element := range elements {
		m[element] = struct{}{}
	}
	return m
}

type OpenSearchHandlerTestSuite struct {
	suite.Suite

	logger             *log.Entry
	mockServiceUsers   *serviceuser.MockServiceUserManager
	mockProject        *project.MockProjectManager
	mockServices       *service.MockServiceManager
	mockOpenSearchACL  *opensearch.MockACLManager
	opensearchHandler  OpenSearchHandler
	applicationBuilder aiven_nais_io_v1.AivenApplicationBuilder
	ctx                context.Context
	cancel             context.CancelFunc
}

func (suite *OpenSearchHandlerTestSuite) SetupSuite() {
	suite.logger = log.NewEntry(log.New())
}

func (suite *OpenSearchHandlerTestSuite) addDefaultMocks(enabled map[int]struct{}) {
	if _, ok := enabled[ServicesGetAddresses]; ok {
		suite.mockServices.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
			Return(&service.ServiceAddresses{
				ServiceURI: serviceURI,
				OpenSearch: service.ServiceAddress{
					URI:  serviceURI,
					Host: serviceHost,
					Port: servicePort,
				},
			}, nil)
	}
	if _, ok := enabled[ServiceUsersGet]; ok {
		suite.mockServiceUsers.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&aiven.ServiceUser{
				Username: serviceUserName,
				Password: servicePassword,
			}, nil)
	}

	if _, ok := enabled[ServiceUsersGet]; ok {
		suite.mockProject.On("GetCA", mock.Anything, mock.Anything).
			Return("my-ca", nil)
	}

	if _, ok := enabled[ServiceUsersCreate]; ok {
		suite.mockServiceUsers.On("Create", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&aiven.ServiceUser{
				Username: serviceUserName,
				Password: servicePassword,
			}, nil)
	}
	if _, ok := enabled[OpenSearchACLGet]; ok {
		suite.mockOpenSearchACL.On("Get", mock.Anything, mock.Anything, mock.Anything).
			Return(&aiven.OpenSearchACLResponse{
				OpenSearchACLConfig: aiven.OpenSearchACLConfig{
					ACLs: []aiven.OpenSearchACL{
						{
							Rules:    nil,
							Username: serviceUserName,
						},
					},
					Enabled:     true,
					ExtendedAcl: false,
				},
			}, nil).Once()
	}
	if _, ok := enabled[OpenSearchACLUpdate]; ok {
		suite.mockOpenSearchACL.On("Update", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&aiven.OpenSearchACLResponse{
				OpenSearchACLConfig: aiven.OpenSearchACLConfig{
					ACLs: []aiven.OpenSearchACL{
						{
							Rules: []aiven.OpenSearchACLRule{
								{Index: "*", Permission: access},
								{Index: "_*", Permission: access},
							},
							Username: serviceUserName,
						},
					},
					Enabled:     true,
					ExtendedAcl: false,
				},
			}, nil).Once()
	}
}

func (suite *OpenSearchHandlerTestSuite) SetupTest() {
	suite.mockServiceUsers = &serviceuser.MockServiceUserManager{}
	suite.mockServices = &service.MockServiceManager{}
	suite.mockProject = &project.MockProjectManager{}
	suite.mockOpenSearchACL = &opensearch.MockACLManager{}
	suite.opensearchHandler = OpenSearchHandler{
		serviceuser:   suite.mockServiceUsers,
		service:       suite.mockServices,
		openSearchACL: suite.mockOpenSearchACL,
		secretHandler: secret.Handler{
			Project:     suite.mockProject,
			ProjectName: projectName,
		},
		projectName: projectName,
	}
	suite.applicationBuilder = aiven_nais_io_v1.NewAivenApplicationBuilder("test-app", namespace)
	suite.ctx, suite.cancel = context.WithTimeout(context.Background(), 5*time.Second)
}

func (suite *OpenSearchHandlerTestSuite) TearDownTest() {
	suite.cancel()
}

func (suite *OpenSearchHandlerTestSuite) TestNoOpenSearch() {
	suite.addDefaultMocks(enabled(ServicesGetAddresses))
	application := suite.applicationBuilder.Build()
	sharedSecret := &v1.Secret{}
	individualSecrets, err := suite.opensearchHandler.Apply(suite.ctx, &application, sharedSecret, suite.logger)

	suite.NoError(err)
	suite.Equal(&v1.Secret{}, sharedSecret)
	suite.Nil(individualSecrets)
}

func (suite *OpenSearchHandlerTestSuite) TestOpenSearchOk() {
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ServiceUsersGet))
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
				Instance: instance,
				Access:   access,
			},
		}).
		Build()
	sharedSecret := &v1.Secret{}
	individualSecrets, err := suite.opensearchHandler.Apply(suite.ctx, &application, sharedSecret, suite.logger)

	suite.NoError(err)
	expected := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				ProjectAnnotation:     projectName,
				ServiceNameAnnotation: instance,
				ServiceUserAnnotation: serviceUserName,
			},
			Finalizers: []string{constants.AivenatorFinalizer},
		},
		// Check these individually
		Data:       sharedSecret.Data,
		StringData: sharedSecret.StringData,
	}
	suite.Equal(expected, sharedSecret)
	suite.ElementsMatch(utils.KeysFromStringMap(sharedSecret.StringData), []string{
		OpenSearchUser, OpenSearchPassword, OpenSearchURI, OpenSearchHost, OpenSearchPort,
	})
	suite.Nil(individualSecrets)
}

func (suite *OpenSearchHandlerTestSuite) TestOpenSearchIndividualSecretsOk() {
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ServiceUsersGet))
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
				Instance:   instance,
				Access:     access,
				SecretName: "foo",
			},
		}).
		Build()
	sharedSecret := &v1.Secret{}
	individualSecrets, err := suite.opensearchHandler.Apply(suite.ctx, &application, sharedSecret, suite.logger)

	suite.NoError(err)
	expected := v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      application.Spec.OpenSearch.SecretName,
			Namespace: application.GetNamespace(),
			Annotations: map[string]string{
				ProjectAnnotation:                 projectName,
				ServiceNameAnnotation:             instance,
				ServiceUserAnnotation:             serviceUserName,
				constants.AivenatorProtectedKey:   "false",
				"nais.io/deploymentCorrelationID": "",
			},
			Labels:     individualSecrets[0].ObjectMeta.Labels,
			Finalizers: []string{constants.AivenatorFinalizer},
		},
		// Check these individually
		Data:       individualSecrets[0].Data,
		StringData: individualSecrets[0].StringData,
	}
	suite.Len(sharedSecret.StringData, 0)
	suite.Len(individualSecrets, 1)
	suite.Equal(expected, individualSecrets[0])
	suite.ElementsMatch(utils.KeysFromStringMap(individualSecrets[0].StringData), []string{
		OpenSearchUser, OpenSearchPassword, OpenSearchURI, OpenSearchHost, OpenSearchPort, secret.AivenCAKey, secret.AivenSecretUpdatedKey,
	})
}

func (suite *OpenSearchHandlerTestSuite) TestServiceGetFailed() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
				Instance: instance,
				Access:   access,
			},
		}).
		Build()
	secret := &v1.Secret{}
	suite.addDefaultMocks(enabled(ServiceUsersGet))
	suite.mockServices.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, aiven.Error{
			Message:  "aiven-error",
			MoreInfo: "aiven-more-info",
			Status:   500,
		})

	individualSecrets, err := suite.opensearchHandler.Apply(suite.ctx, &application, secret, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure))
	suite.Nil(individualSecrets)
}

func (suite *OpenSearchHandlerTestSuite) TestServiceUsersGetFailed() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
				Instance: instance,
				Access:   access,
			},
		}).
		Build()
	sharedSecret := &v1.Secret{}
	suite.addDefaultMocks(enabled(ServicesGetAddresses))
	suite.mockServiceUsers.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, aiven.Error{
			Message:  "aiven-error",
			MoreInfo: "aiven-more-info",
			Status:   500,
		})

	individualSecrets, err := suite.opensearchHandler.Apply(suite.ctx, &application, sharedSecret, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure))
	suite.Nil(individualSecrets)
}

func (suite *OpenSearchHandlerTestSuite) TestServiceUserCreateFailed() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
				Instance: instance,
				Access:   access,
			},
		}).
		Build()
	username := serviceUserName + "-r-3D_"

	suite.addDefaultMocks(enabled(ServicesGetAddresses))
	suite.mockServiceUsers.On("Get", mock.Anything, username, projectName, mock.Anything, mock.Anything).
		Return(nil, aiven.Error{
			Message: "Service user does not exist",
			Status:  404,
		})
	suite.mockServiceUsers.On("Create", mock.Anything, username, projectName, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, aiven.Error{
			Message:  "aiven-error",
			MoreInfo: "aiven-more-info",
			Status:   500,
		}).Once()

	sharedSecret := &v1.Secret{}
	individualSecrets, err := suite.opensearchHandler.Apply(suite.ctx, &application, sharedSecret, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure))
	suite.Nil(individualSecrets)
}

func (suite *OpenSearchHandlerTestSuite) TestServiceUserCreatedIfNeeded() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
				Instance: instance,
				Access:   access,
			},
		}).
		Build()
	username := serviceUserName + "-r-3D_"

	suite.addDefaultMocks(enabled(ServicesGetAddresses, OpenSearchACLGet, OpenSearchACLUpdate))
	suite.mockServiceUsers.On("Get", mock.Anything, username, projectName, mock.Anything, mock.Anything).
		Return(nil, aiven.Error{
			Message: "Service user does not exist",
			Status:  404,
		})
	suite.mockServiceUsers.On("Create", mock.Anything, username, projectName, mock.Anything, mock.Anything, mock.Anything).
		Return(&aiven.ServiceUser{
			Username: username,
			Password: servicePassword,
		}, nil).Once()

	sharedSecret := &v1.Secret{}
	individualSecrets, err := suite.opensearchHandler.Apply(suite.ctx, &application, sharedSecret, suite.logger)

	suite.NoError(err)
	suite.Equal(username, sharedSecret.StringData[OpenSearchUser])
	suite.mockServiceUsers.AssertExpectations(suite.T())
	suite.mockServices.AssertExpectations(suite.T())
	suite.mockOpenSearchACL.AssertExpectations(suite.T())
	suite.Nil(individualSecrets)
}

func (suite *OpenSearchHandlerTestSuite) TestCorrectServiceUserSelected() {
	testData := []struct {
		access   string
		username string
	}{
		{
			access:   "read",
			username: serviceUserName + "-r-3D_",
		},
		{
			access:   "readwrite",
			username: serviceUserName + "-rw-3D_",
		},
		{
			access:   "write",
			username: serviceUserName + "-w-3D_",
		},
		{
			access:   "admin",
			username: serviceUserName,
		},
	}

	for _, t := range testData {
		suite.Run(t.access, func() {
			suite.addDefaultMocks(enabled(ServicesGetAddresses))
			suite.mockServiceUsers.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return(&aiven.ServiceUser{
					Username: t.username,
					Password: servicePassword,
				}, nil).Once()
			application := suite.applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
						Instance: instance,
						Access:   t.access,
					},
				}).
				Build()
			sharedSecret := &v1.Secret{}
			individualSecrets, err := suite.opensearchHandler.Apply(suite.ctx, &application, sharedSecret, suite.logger)

			suite.NoError(err)
			suite.Equal(t.username, sharedSecret.StringData[OpenSearchUser])
			suite.Nil(individualSecrets)
		})
	}
}

func TestOpenSearchHandler(t *testing.T) {
	opensearchTestSuite := new(OpenSearchHandlerTestSuite)
	suite.Run(t, opensearchTestSuite)
}
