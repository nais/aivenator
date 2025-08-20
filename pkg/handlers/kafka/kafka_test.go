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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
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

type mockContainer struct {
	projectManager     *project.MockProjectManager
	serviceUserManager *serviceuser.MockServiceUserManager
	serviceManager     *service.MockServiceManager
	nameResolver       *liberator_service.MockNameResolver
}

func enabled(elements ...int) map[int]struct{} {
	m := make(map[int]struct{}, len(elements))
	for _, element := range elements {
		m[element] = struct{}{}
	}
	return m
}

func TestKafka(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Kafka Suite")
}

var _ = Describe("kafka handler", func() {
	var mocks mockContainer
	var logger log.FieldLogger
	var applicationBuilder aiven_nais_io_v1.AivenApplicationBuilder
	var ctx context.Context
	var sharedSecret corev1.Secret
	var cancel context.CancelFunc
	var kafkaHandler KafkaHandler
	//var application aiven_nais_io_v1.AivenApplication

	BeforeEach(func() {
		sharedSecret = corev1.Secret{}

		root := log.New()
		root.Out = GinkgoWriter
		logger = log.NewEntry(root)
		mocks = mockContainer{
			projectManager:     project.NewMockProjectManager(GinkgoT()),
			serviceUserManager: serviceuser.NewMockServiceUserManager(GinkgoT()),
			serviceManager:     service.NewMockServiceManager(GinkgoT()),
			nameResolver:       liberator_service.NewMockNameResolver(GinkgoT()),
		}
		kafkaHandler = KafkaHandler{
			project:      mocks.projectManager,
			serviceuser:  mocks.serviceUserManager,
			service:      mocks.serviceManager,
			generator:    nil,
			nameResolver: mocks.nameResolver,
			projects:     nil,
		}
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)

		applicationBuilder = aiven_nais_io_v1.NewAivenApplicationBuilder("test-app", "test-ns")
	})
	AfterEach(func() {
		cancel()
	})

	When("no kafka is configured", func() {
		It("no error on cleanup", func() {
			err := kafkaHandler.Cleanup(ctx, &sharedSecret, logger)

			Expect(err).ToNot(HaveOccurred())
		})
		Context("delete serviceUser on cleanup", func() {
			BeforeEach(func() {
				sharedSecret.SetAnnotations(map[string]string{
					ServiceUserAnnotation: serviceUserName,
					PoolAnnotation:        pool,
				})
				mocks.serviceUserManager.On("Delete", mock.Anything, serviceUserName, pool, mock.Anything, mock.Anything).Return(nil)
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, "my-testing-pool").Return("kafka", nil)

			})
			It("should not error", func() {
				err := kafkaHandler.Cleanup(ctx, &sharedSecret, logger)
				Expect(err).ToNot(HaveOccurred())
			})
		})
		Context("delete serviceUser on cleanup, but already gone", func() {
			BeforeEach(func() {
				sharedSecret.SetAnnotations(map[string]string{
					ServiceUserAnnotation: serviceUserName,
					PoolAnnotation:        pool,
				})
				mocks.serviceUserManager.On("Delete", mock.Anything, serviceUserName, pool, mock.Anything, mock.Anything).Return(aiven.Error{
					Message: "Not Found",
					Status:  404,
				})
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, "my-testing-pool").Return("kafka", nil)
			})
			It("should not return an error", func() {
				err := kafkaHandler.Cleanup(ctx, &sharedSecret, logger)
				Expect(err).ToNot(HaveOccurred())
			})
		})
	})
	When("there is an aiven application", func() {
		Context("that has no kafka configured", func() {
			It("should not return an error", func() {
				application := applicationBuilder.Build()
				individualSecrets, err := kafkaHandler.Apply(ctx, &application, &sharedSecret, logger)

				Expect(err).ToNot(HaveOccurred())
				Expect(individualSecrets).To(BeNil())
				Expect(sharedSecret).To(Equal(corev1.Secret{}))
			})
		})
	})
})

type KafkaHandlerTestSuite struct {
	suite.Suite

	logger             *log.Entry
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

func (suite *KafkaHandlerTestSuite) TestKafkaOk() {
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ProjectGetCA, ServiceUsersCreate, GeneratorMakeCredStores, ServiceUsersGetNotFound))
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Kafka: &aiven_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()
	sharedSecret := &corev1.Secret{}
	individualSecrets, err := suite.kafkaHandler.Apply(suite.ctx, &application, sharedSecret, suite.logger)

	suite.NoError(err)
	expected := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				ServiceUserAnnotation: serviceUserName,
				PoolAnnotation:        pool,
			},
			Finalizers: []string{constants.AivenatorFinalizer},
		},
		// Check these individually
		Data:       sharedSecret.Data,
		StringData: sharedSecret.StringData,
	}
	suite.Equal(expected, sharedSecret)
	suite.Nil(individualSecrets)

	suite.ElementsMatch(utils.KeysFromStringMap(sharedSecret.StringData), []string{
		KafkaCA, KafkaPrivateKey, KafkaCredStorePassword, KafkaSchemaRegistry, KafkaSchemaUser, KafkaSchemaPassword,
		KafkaBrokers, KafkaSecretUpdated, KafkaCertificate,
	})
	suite.ElementsMatch(keysFromByteMap(sharedSecret.Data), []string{KafkaKeystore, KafkaTruststore})
}

func (suite *KafkaHandlerTestSuite) TestSecretExists() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Kafka: &aiven_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()
	sharedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				ServiceUserAnnotation: serviceUserName,
			},
		},
	}
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ProjectGetCA, GeneratorMakeCredStores, ServiceUsersGet))

	individualSecrets, err := suite.kafkaHandler.Apply(suite.ctx, &application, sharedSecret, suite.logger)

	suite.NoError(err)
	suite.Empty(validation.ValidateAnnotations(sharedSecret.GetAnnotations(), field.NewPath("metadata.annotations")))
	suite.Equal(sharedSecret.GetAnnotations()[ServiceUserAnnotation], serviceUserName)
	suite.Nil(individualSecrets)
}

func (suite *KafkaHandlerTestSuite) TestServiceGetFailed() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Kafka: &aiven_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()
	sharedSecret := &corev1.Secret{}
	suite.addDefaultMocks(enabled(ProjectGetCA, ServiceUsersCreate, GeneratorMakeCredStores))
	suite.mockServices.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, aiven.Error{
			Message:  "aiven-error",
			MoreInfo: "aiven-more-info",
			Status:   500,
		})

	individualSecrets, err := suite.kafkaHandler.Apply(suite.ctx, &application, sharedSecret, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure))
	suite.Nil(individualSecrets)
}

func (suite *KafkaHandlerTestSuite) TestProjectGetCAFailed() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Kafka: &aiven_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()
	sharedSecret := &corev1.Secret{}
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ServiceUsersCreate, GeneratorMakeCredStores))
	suite.mockProjects.On("GetCA", mock.Anything, mock.Anything).
		Return("", aiven.Error{
			Message:  "aiven-error",
			MoreInfo: "aiven-more-info",
			Status:   500,
		})

	individualSecrets, err := suite.kafkaHandler.Apply(suite.ctx, &application, sharedSecret, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure))
	suite.Nil(individualSecrets)
}

func (suite *KafkaHandlerTestSuite) TestServiceUsersCreateFailed() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Kafka: &aiven_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()
	sharedSecret := &corev1.Secret{}
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ProjectGetCA, GeneratorMakeCredStores, ServiceUsersGetNotFound))
	suite.mockServiceUsers.On("Create", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, aiven.Error{
			Message:  "aiven-error",
			MoreInfo: "aiven-more-info",
			Status:   500,
		})

	individualSecrets, err := suite.kafkaHandler.Apply(suite.ctx, &application, sharedSecret, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure))
	suite.Nil(individualSecrets)
}

func (suite *KafkaHandlerTestSuite) TestServiceUserNotFound() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Kafka: &aiven_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()
	sharedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				ServiceUserAnnotation: serviceUserName,
			},
		},
	}
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ProjectGetCA, ServiceUsersCreate, GeneratorMakeCredStores, ServiceUsersGetNotFound))

	individualSecrets, err := suite.kafkaHandler.Apply(suite.ctx, &application, sharedSecret, suite.logger)

	suite.NoError(err)
	expected := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				ServiceUserAnnotation: serviceUserName,
				PoolAnnotation:        pool,
			},
			Finalizers: []string{constants.AivenatorFinalizer},
		},
		Data:       sharedSecret.Data,
		StringData: sharedSecret.StringData,
	}
	suite.Equal(expected, sharedSecret)
	suite.Nil(individualSecrets)
}

func (suite *KafkaHandlerTestSuite) TestServiceUserCollision() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Kafka: &aiven_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()
	sharedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{},
		},
	}
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ProjectGetCA, GeneratorMakeCredStores))
	suite.mockServiceUsers.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&aiven.ServiceUser{
			Username: serviceUserName,
		}, nil)

	individualSecrets, err := suite.kafkaHandler.Apply(suite.ctx, &application, sharedSecret, suite.logger)

	suite.mockServiceUsers.AssertNotCalled(suite.T(), "Create", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	suite.NoError(err)
	expected := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				ServiceUserAnnotation: serviceUserName,
				PoolAnnotation:        pool,
			},
			Finalizers: []string{constants.AivenatorFinalizer},
		},
		Data:       sharedSecret.Data,
		StringData: sharedSecret.StringData,
	}
	suite.Equal(expected, sharedSecret)
	suite.Nil(individualSecrets)
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
	secret := &corev1.Secret{}
	individualSecrets, err := suite.kafkaHandler.Apply(suite.ctx, &application, secret, suite.logger)

	suite.Error(err)
	suite.True(errors.Is(err, utils.ErrUnrecoverable))
	suite.Nil(individualSecrets)
}

func (suite *KafkaHandlerTestSuite) TestGeneratorMakeCredStoresFailed() {
	application := suite.applicationBuilder.
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			Kafka: &aiven_nais_io_v1.KafkaSpec{
				Pool: pool,
			},
		}).
		Build()
	sharedSecret := &corev1.Secret{}
	suite.addDefaultMocks(enabled(ServicesGetAddresses, ProjectGetCA, ServiceUsersCreate, ServiceUsersGetNotFound))
	suite.mockGenerator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("local-fail"))

	individualSecrets, err := suite.kafkaHandler.Apply(suite.ctx, &application, sharedSecret, suite.logger)

	suite.Error(err)
	suite.NotNil(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationLocalFailure))
	suite.Nil(individualSecrets)
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
