package opensearch

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/aiven/opensearch"
	"github.com/nais/aivenator/pkg/aiven/project"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/handlers/secret"
	"github.com/nais/aivenator/pkg/utils"

	corev1 "k8s.io/api/core/v1"
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
	secretName      = "foo"
)

const (
	ServicesGetAddresses = iota
	ServiceUsersGet
	ServiceUsersCreate
	OpenSearchACLGet
	OpenSearchACLUpdate
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

	applicationBuilder aiven_nais_io_v1.AivenApplicationBuilder
	cancel             context.CancelFunc
	ctx                context.Context
	logger             log.FieldLogger
	mockOpenSearchACL  *opensearch.MockACLManager
	mockProjects       *project.MockProjectManager
	mockServiceUsers   *serviceuser.MockServiceUserManager
	mockServices       *service.MockServiceManager
	opensearchHandler  OpenSearchHandler
	mockSecrets        *secret.MockSecrets
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
	suite.mockOpenSearchACL = &opensearch.MockACLManager{}
	suite.mockSecrets = &secret.MockSecrets{}
	suite.opensearchHandler = OpenSearchHandler{
		project:        suite.mockProjects,
		serviceuser:    suite.mockServiceUsers,
		service:        suite.mockServices,
		openSearchACL:  suite.mockOpenSearchACL,
		projectName:    projectName,
		secretsHandler: suite.mockSecrets,
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

	nilforsure, err := suite.opensearchHandler.Apply(suite.ctx, &application, suite.logger)
	typedNil := []*corev1.Secret(nil)

	suite.Equal(nilforsure, typedNil) // 🤡
	suite.NoError(err)
}

func (suite *OpenSearchHandlerTestSuite) TestOpenSearchOk() {
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ServiceUsersGet))
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
				Instance:   instance,
				Access:     access,
				SecretName: secretName,
			},
		}).
		Build()
	expected := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				ProjectAnnotation:     projectName,
				ServiceNameAnnotation: fmt.Sprintf("opensearch-%s-%s", application.GetNamespace(), instance),
				ServiceUserAnnotation: serviceUserName,
			},
			Finalizers: []string{constants.AivenatorFinalizer},
		},
		// Check these individua

		StringData: map[string]string{"OPEN_SEARCH_HOST": "example.com", "OPEN_SEARCH_PASSWORD": "service-password", "OPEN_SEARCH_PORT": "1234", "OPEN_SEARCH_URI": "http://example.com:1234", "OPEN_SEARCH_USERNAME": "team-a"},
	}

	suite.mockSecrets.On("NormalizeSecret", mock.Anything, &application, &expected, suite.logger).Return(nil)
	suite.mockSecrets.On("GetOrInitSecret", mock.Anything, namespace, secretName, suite.logger).Return(expected)
	result, err := suite.opensearchHandler.Apply(suite.ctx, &application, suite.logger)

	suite.NoError(err)

	suite.Equal(&expected, result[0])
	suite.ElementsMatch(utils.KeysFromStringMap(expected.StringData), []string{
		OpenSearchUser, OpenSearchPassword, OpenSearchURI, OpenSearchHost, OpenSearchPort,
	})
}

func (suite *OpenSearchHandlerTestSuite) TestServiceGetFailed() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
				Instance:   instance,
				Access:     access,
				SecretName: secretName,
			},
		}).
		Build()
	suite.addDefaultMocks(enabled(ServiceUsersGet))
	suite.mockServices.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, aiven.Error{
			Message:  "aiven-error",
			MoreInfo: "aiven-more-info",
			Status:   500,
		})
	suite.mockSecrets.On("NormalizeSecret", mock.Anything, &application, &corev1.Secret{}, suite.logger).Return(nil)
	suite.mockSecrets.On("GetOrInitSecret", mock.Anything, namespace, secretName, suite.logger).Return(corev1.Secret{})

	_, err := suite.opensearchHandler.Apply(suite.ctx, &application, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure))
}

func (suite *OpenSearchHandlerTestSuite) TestServiceUsersGetFailed() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
				Instance:   instance,
				Access:     access,
				SecretName: secretName,
			},
		}).
		Build()
	suite.addDefaultMocks(enabled(ServicesGetAddresses))
	suite.mockServiceUsers.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, aiven.Error{
			Message:  "aiven-error",
			MoreInfo: "aiven-more-info",
			Status:   500,
		})
	suite.mockSecrets.On("NormalizeSecret", mock.Anything, &application, &corev1.Secret{}, suite.logger).Return(nil)
	suite.mockSecrets.On("GetOrInitSecret", mock.Anything, namespace, secretName, suite.logger).Return(corev1.Secret{})

	_, err := suite.opensearchHandler.Apply(suite.ctx, &application, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure))
}

func (suite *OpenSearchHandlerTestSuite) TestServiceUserCreateFailed() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
				Instance:   instance,
				Access:     access,
				SecretName: secretName,
			},
		}).
		Build()
	username := serviceUserName + "-r-9Nv"

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
	suite.mockSecrets.On("NormalizeSecret", mock.Anything, &application, &corev1.Secret{}, suite.logger).Return(nil)
	suite.mockSecrets.On("GetOrInitSecret", mock.Anything, namespace, secretName, suite.logger).Return(corev1.Secret{})

	_, err := suite.opensearchHandler.Apply(suite.ctx, &application, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure))
}

func (suite *OpenSearchHandlerTestSuite) TestServiceUserCreatedIfNeeded() {
	username := serviceUserName + "-r-9Nv"
	secretName := strings.ToLower(username)
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			SecretName: secretName,
			OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{},
		}).
		Build()

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
	expectedSecret := corev1.Secret{
		StringData: map[string]string{OpenSearchUser: username},
		ObjectMeta: metav1.ObjectMeta{Name: secretName},
	}
	suite.mockSecrets.On("GetOrInitSecret", mock.Anything, namespace, secretName, suite.logger).Return(expectedSecret)
	suite.mockSecrets.On("NormalizeSecret", mock.Anything, &application, &expectedSecret, suite.logger).Return(nil)

	secrets, err := suite.opensearchHandler.Apply(suite.ctx, &application, suite.logger)

	suite.NoError(err)
	suite.Equal(len(secrets), 1)
	suite.Equal(username, secrets[0].StringData[OpenSearchUser])
	suite.mockServiceUsers.AssertExpectations(suite.T())
	suite.mockServices.AssertExpectations(suite.T())
	suite.mockOpenSearchACL.AssertExpectations(suite.T())
}

func (suite *OpenSearchHandlerTestSuite) TestCorrectServiceUserSelected() {
	testData := []struct {
		access   string
		username string
	}{
		{
			access:   "read",
			username: serviceUserName + "-r-9Nv",
		},
		{
			access:   "readwrite",
			username: serviceUserName + "-rw-9Nv",
		},
		{
			access:   "write",
			username: serviceUserName + "-w-9Nv",
		},
		{
			access:   "admin",
			username: serviceUserName,
		},
	}

	for _, t := range testData {
		suite.Run(t.access, func() {

			suite.addDefaultMocks(enabled(ServicesGetAddresses))
			suite.mockServiceUsers.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&aiven.ServiceUser{
				Username: t.username,
				Password: servicePassword,
			}, nil).Once()
			secretName := strings.ToLower(t.username)
			application := suite.applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					SecretName: secretName,
					OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
						Instance: instance,
						Access:   t.access,
					},
				}).
				Build()
			expectedSecret := corev1.Secret{
				StringData: map[string]string{OpenSearchUser: t.username},
			}
			suite.mockSecrets.On("NormalizeSecret", mock.Anything, &application, &expectedSecret, suite.logger).Return(nil)
			suite.mockSecrets.On("GetOrInitSecret", mock.Anything, namespace, secretName, suite.logger).Return(expectedSecret)

			secrets, err := suite.opensearchHandler.Apply(suite.ctx, &application, suite.logger)

			suite.NoError(err)
			suite.Equal(len(secrets), 1)
			suite.Equal(t.username, secrets[0].StringData[OpenSearchUser])
		})
	}
}

func TestOpenSearchHandler(t *testing.T) {
	opensearchTestSuite := new(OpenSearchHandlerTestSuite)
	suite.Run(t, opensearchTestSuite)
}
