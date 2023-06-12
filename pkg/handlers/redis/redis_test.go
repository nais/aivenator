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
	servicePassword = "service-password"
	projectName     = "my-project"
)

type testData struct {
	instanceName             string
	serviceName              string
	serviceURI               string
	access                   string
	username                 string
	serviceUserAnnotationKey string
	usernameKey              string
	passwordKey              string
	uriKey                   string
}

var testInstances = []testData{
	{
		instanceName:             "my-instance1",
		serviceName:              "redis-team-a-my-instance1",
		serviceURI:               "rediss://my-instance1.example.com:23456",
		access:                   "read",
		username:                 "test-app-r",
		serviceUserAnnotationKey: "my_instance1.redis.aiven.nais.io/serviceUser",
		usernameKey:              "REDIS_USERNAME_MY_INSTANCE1",
		passwordKey:              "REDIS_PASSWORD_MY_INSTANCE1",
		uriKey:                   "REDIS_URI_MY_INSTANCE1",
	},
	{
		instanceName:             "session-store",
		serviceName:              "redis-team-a-session-store",
		serviceURI:               "rediss://session-store.example.com:23456",
		access:                   "readwrite",
		username:                 "test-app-rw",
		serviceUserAnnotationKey: "session_store.redis.aiven.nais.io/serviceUser",
		usernameKey:              "REDIS_USERNAME_SESSION_STORE",
		passwordKey:              "REDIS_PASSWORD_SESSION_STORE",
		uriKey:                   "REDIS_URI_SESSION_STORE",
	},
}

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

	defaultServiceManagerMock := func(data testData) {
		mocks.serviceManager.On("GetServiceAddresses", projectName, data.serviceName).
			Return(&service.ServiceAddresses{
				Redis: data.serviceURI,
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
		data := testInstances[0]

		BeforeEach(func() {
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Redis: []*aiven_nais_io_v1.RedisSpec{
						{
							Instance: data.instanceName,
							Access:   data.access,
						},
					}}).
				Build()
		})

		Context("and the service is unavailable", func() {
			BeforeEach(func() {
				mocks.serviceManager.On("GetServiceAddresses", projectName, data.serviceName).
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
				defaultServiceManagerMock(data)
				mocks.serviceUserManager.On("Get", data.username, projectName, data.serviceName, mock.Anything).
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
		data := testInstances[0]

		BeforeEach(func() {
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Redis: []*aiven_nais_io_v1.RedisSpec{
						{
							Instance: data.instanceName,
							Access:   data.access,
						},
					}}).
				Build()
		})

		assertHappy := func(secret *v1.Secret, err error) {
			GinkgoHelper()
			Expect(err).To(Succeed())
			Expect(secret.GetAnnotations()).To(HaveKeyWithValue(ProjectAnnotation, projectName))
			Expect(secret.GetAnnotations()).To(HaveKeyWithValue(data.serviceUserAnnotationKey, data.username))
			Expect(secret.StringData).To(HaveKeyWithValue(data.usernameKey, data.username))
			Expect(secret.StringData).To(HaveKeyWithValue(data.passwordKey, servicePassword))
			Expect(secret.StringData).To(HaveKeyWithValue(data.uriKey, data.serviceURI))
		}

		Context("and the service user already exists", func() {
			BeforeEach(func() {
				defaultServiceManagerMock(data)
				mocks.serviceUserManager.On("Get", data.username, projectName, data.serviceName, mock.Anything).
					Return(&aiven.ServiceUser{
						Username: data.username,
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
				defaultServiceManagerMock(data)
				mocks.serviceUserManager.On("Get", data.username, projectName, data.serviceName, mock.Anything).
					Return(nil, aiven.Error{
						Message: "Service user does not exist",
						Status:  404,
					})
				accessControl := &aiven.AccessControl{
					RedisACLCategories: getRedisACLCategories(data.access),
					RedisACLKeys:       []string{"*"},
					RedisACLChannels:   []string{"*"},
				}
				mocks.serviceUserManager.On("Create", data.username, projectName, data.serviceName, accessControl, mock.Anything).
					Return(&aiven.ServiceUser{
						Username: data.username,
						Password: servicePassword,
					}, nil)
			})

			It("creates the new user and returns credentials for the new user", func() {
				err := redisHandler.Apply(&application, &secret, logger)
				assertHappy(&secret, err)
			})
		})
	})

	When("it receives a spec with multiple instances", func() {
		BeforeEach(func() {
			var specs []*aiven_nais_io_v1.RedisSpec
			for _, data := range testInstances {
				specs = append(specs, &aiven_nais_io_v1.RedisSpec{
					Instance: data.instanceName,
					Access:   data.access,
				})
			}
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Redis: specs,
				}).
				Build()
		})

		assertHappy := func(secret *v1.Secret, data testData, err error) {
			GinkgoHelper()
			Expect(err).To(Succeed())
			Expect(secret.GetAnnotations()).To(HaveKeyWithValue(ProjectAnnotation, projectName))
			Expect(secret.GetAnnotations()).To(HaveKeyWithValue(data.serviceUserAnnotationKey, data.username))
			Expect(secret.StringData).To(HaveKeyWithValue(data.usernameKey, data.username))
			Expect(secret.StringData).To(HaveKeyWithValue(data.passwordKey, servicePassword))
			Expect(secret.StringData).To(HaveKeyWithValue(data.uriKey, data.serviceURI))
		}

		Context("and the service user already exists", func() {
			BeforeEach(func() {
				for _, data := range testInstances {
					defaultServiceManagerMock(data)
					mocks.serviceUserManager.On("Get", data.username, projectName, data.serviceName, mock.Anything).
						Return(&aiven.ServiceUser{
							Username: data.username,
							Password: servicePassword,
						}, nil)
				}
			})

			It("uses the existing user", func() {
				err := redisHandler.Apply(&application, &secret, logger)
				for _, data := range testInstances {
					assertHappy(&secret, data, err)
				}
			})
		})

		Context("and the service user doesn't exist", func() {
			BeforeEach(func() {
				for _, data := range testInstances {
					defaultServiceManagerMock(data)
					mocks.serviceUserManager.On("Get", data.username, projectName, data.serviceName, mock.Anything).
						Return(nil, aiven.Error{
							Message: "Service user does not exist",
							Status:  404,
						})
					accessControl := &aiven.AccessControl{
						RedisACLCategories: getRedisACLCategories(data.access),
						RedisACLKeys:       []string{"*"},
						RedisACLChannels:   []string{"*"},
					}
					mocks.serviceUserManager.On("Create", data.username, projectName, data.serviceName, accessControl, mock.Anything).
						Return(&aiven.ServiceUser{
							Username: data.username,
							Password: servicePassword,
						}, nil)
				}
			})

			It("creates the new user and returns credentials for the new user", func() {
				err := redisHandler.Apply(&application, &secret, logger)
				for _, data := range testInstances {
					assertHappy(&secret, data, err)
				}
			})
		})
	})
})
