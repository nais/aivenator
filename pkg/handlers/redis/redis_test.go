package redis

import (
	aivenator_mocks "github.com/nais/aivenator/pkg/mocks"
	"testing"

	"github.com/aiven/aiven-go-client"
	"github.com/nais/aivenator/pkg/aiven/service"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	v1 "k8s.io/api/core/v1"
)

const (
	appName         = "test-app"
	namespace       = "team-a"
	readUser        = "test-app-r"
	servicePassword = "service-password"
	projectName     = "my-project"
	serviceName     = "redis-team-a-my-instance"
	serviceURI      = "rediss://example.com:23456"
	instance        = "my-instance"
	access          = "read"
)

type mockContainer struct {
	serviceUserManager *aivenator_mocks.ServiceUserManager
	serviceManager     *aivenator_mocks.ServiceManager
}

func TestRedis(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Redis Suite")
}

var _ = Describe("redis.Handler", func() {
	var logger log.FieldLogger
	var applicationBuilder aiven_nais_io_v1.AivenApplicationBuilder
	var application aiven_nais_io_v1.AivenApplication
	var secret v1.Secret
	var redisHandler RedisHandler
	var mocks mockContainer

	defaultServiceManagerMock := func() {
		mocks.serviceManager.On("GetServiceAddresses", projectName, serviceName).
			Return(&service.ServiceAddresses{
				Redis: serviceURI,
			}, nil)
	}

	BeforeEach(func() {
		root := log.New()
		root.Out = GinkgoWriter
		logger = log.NewEntry(root)
		applicationBuilder = aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace)
		secret = v1.Secret{}
		mocks = mockContainer{
			serviceUserManager: aivenator_mocks.NewServiceUserManager(GinkgoT()),
			serviceManager:     aivenator_mocks.NewServiceManager(GinkgoT()),
		}
		redisHandler = RedisHandler{
			serviceuser: mocks.serviceUserManager,
			service:     mocks.serviceManager,
			projectName: projectName,
		}
	})

	When("it receives a spec without Redis", func() {
		BeforeEach(func() {
			application = applicationBuilder.Build()
		})

		It("ignores it", func() {
			err := redisHandler.Apply(&application, &secret, logger)
			Expect(err).To(Succeed())
			Expect(secret).To(Equal(v1.Secret{}))
		})
	})

	When("it receives a spec with Redis requested", func() {
		BeforeEach(func() {
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Redis: &aiven_nais_io_v1.RedisSpec{
						Instance: instance,
						Access:   access,
					}}).
				Build()
		})

		Context("and the service is unavailable", func() {
			BeforeEach(func() {
				mocks.serviceManager.On("GetServiceAddresses", projectName, serviceName).
					Return(nil, aiven.Error{
						Message:  "aiven-error",
						MoreInfo: "aiven-more-info",
						Status:   500,
					})
			})

			It("sets the correct aiven fail condition", func() {
				err := redisHandler.Apply(&application, &secret, logger)
				Expect(err).ToNot(Succeed())
				Expect(err).To(MatchError("operation GetService failed in Aiven: 500: aiven-error - aiven-more-info"))
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
			})
		})

		Context("and service users are unavailable", func() {
			BeforeEach(func() {
				defaultServiceManagerMock()
				mocks.serviceUserManager.On("Get", readUser, projectName, serviceName, mock.Anything).
					Return(nil, aiven.Error{
						Message:  "aiven-error",
						MoreInfo: "aiven-more-info",
						Status:   500,
					})
			})

			It("sets the correct aiven fail condition", func() {
				err := redisHandler.Apply(&application, &secret, logger)
				Expect(err).ToNot(Succeed())
				Expect(err).To(MatchError("operation GetServiceUser failed in Aiven: 500: aiven-error - aiven-more-info"))
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
			})
		})
	})

	When("it receives a spec", func() {
		BeforeEach(func() {
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Redis: &aiven_nais_io_v1.RedisSpec{
						Instance: instance,
						Access:   access,
					}}).
				Build()
		})

		assertHappy := func(secret *v1.Secret, err error) {
			GinkgoHelper()
			Expect(err).To(Succeed())
			Expect(secret.GetAnnotations()).To(HaveKeyWithValue(ProjectAnnotation, projectName))
			Expect(secret.GetAnnotations()).To(HaveKeyWithValue(ServiceUserAnnotation, readUser))
			Expect(secret.StringData).To(HaveKeyWithValue(RedisUser, readUser))
			Expect(secret.StringData).To(HaveKeyWithValue(RedisPassword, servicePassword))
			Expect(secret.StringData).To(HaveKeyWithValue(RedisURI, serviceURI))
		}

		Context("and the service user already exists", func() {
			BeforeEach(func() {
				defaultServiceManagerMock()
				mocks.serviceUserManager.On("Get", readUser, projectName, serviceName, mock.Anything).
					Return(&aiven.ServiceUser{
						Username: readUser,
						Password: servicePassword,
					}, nil)
			})

			It("uses the existing user", func() {
				err := redisHandler.Apply(&application, &secret, logger)
				assertHappy(&secret, err)
			})
		})

		Context("and the service user doesn't exist", func() {
			BeforeEach(func() {
				defaultServiceManagerMock()
				mocks.serviceUserManager.On("Get", readUser, projectName, serviceName, mock.Anything).
					Return(nil, aiven.Error{
						Message: "Service user does not exist",
						Status:  404,
					})
				accessControl := &aiven.AccessControl{
					RedisACLCategories: getRedisACLCategories(access),
					RedisACLKeys:       []string{"*"},
					RedisACLChannels:   []string{"*"},
				}
				mocks.serviceUserManager.On("Create", readUser, projectName, serviceName, accessControl, mock.Anything).
					Return(&aiven.ServiceUser{
						Username: readUser,
						Password: servicePassword,
					}, nil)
			})

			It("creates the new user and returns credentials for the new user", func() {
				err := redisHandler.Apply(&application, &secret, logger)
				assertHappy(&secret, err)
			})
		})
	})
})
