package redis

import (
	"context"
	"testing"
	"time"

	"github.com/nais/aivenator/pkg/aiven/serviceuser"

	"github.com/aiven/aiven-go-client/v2"
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
	serviceHost              string
	servicePort              int
	access                   string
	username                 string
	serviceUserAnnotationKey string
	usernameKey              string
	passwordKey              string
	uriKey                   string
	hostKey                  string
	portKey                  string
}

var testInstances = []testData{
	{
		instanceName:             "my-instance1",
		serviceName:              "redis-team-a-my-instance1",
		serviceURI:               "rediss://my-instance1.example.com:23456",
		serviceHost:              "my-instance1.example.com",
		servicePort:              23456,
		access:                   "read",
		username:                 "test-app-r",
		serviceUserAnnotationKey: "my-instance1.redis.aiven.nais.io/serviceUser",
		usernameKey:              "REDIS_USERNAME_MY_INSTANCE1",
		passwordKey:              "REDIS_PASSWORD_MY_INSTANCE1",
		uriKey:                   "REDIS_URI_MY_INSTANCE1",
		hostKey:                  "REDIS_HOST_MY_INSTANCE1",
		portKey:                  "REDIS_PORT_MY_INSTANCE1",
	},
	{
		instanceName:             "session-store",
		serviceName:              "redis-team-a-session-store",
		serviceURI:               "rediss://session-store.example.com:23456",
		serviceHost:              "session-store.example.com",
		servicePort:              23456,
		access:                   "readwrite",
		username:                 "test-app-rw",
		serviceUserAnnotationKey: "session-store.redis.aiven.nais.io/serviceUser",
		usernameKey:              "REDIS_USERNAME_SESSION_STORE",
		passwordKey:              "REDIS_PASSWORD_SESSION_STORE",
		uriKey:                   "REDIS_URI_SESSION_STORE",
		hostKey:                  "REDIS_HOST_SESSION_STORE",
		portKey:                  "REDIS_PORT_SESSION_STORE",
	},
}

type mockContainer struct {
	serviceUserManager *serviceuser.MockServiceUserManager
	serviceManager     *service.MockServiceManager
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
	var ctx context.Context
	var cancel context.CancelFunc

	defaultServiceManagerMock := func(data testData) {
		mocks.serviceManager.On("GetServiceAddresses", mock.Anything, projectName, data.serviceName).
			Return(&service.ServiceAddresses{
				Redis: service.ServiceAddress{
					URI:  data.serviceURI,
					Host: data.serviceHost,
					Port: data.servicePort,
				},
			}, nil)
	}

	BeforeEach(func() {
		root := log.New()
		root.Out = GinkgoWriter
		logger = log.NewEntry(root)
		applicationBuilder = aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace)
		secret = v1.Secret{}
		mocks = mockContainer{
			serviceUserManager: serviceuser.NewMockServiceUserManager(GinkgoT()),
			serviceManager:     service.NewMockServiceManager(GinkgoT()),
		}
		redisHandler = RedisHandler{
			serviceuser: mocks.serviceUserManager,
			service:     mocks.serviceManager,
			projectName: projectName,
		}
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	})

	AfterEach(func() {
		cancel()
	})

	When("it receives a spec without Redis", func() {
		BeforeEach(func() {
			application = applicationBuilder.Build()
		})

		It("ignores it", func() {
			_, err := redisHandler.Apply(ctx, &application, logger)
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
					},
				}).
				Build()
		})

		Context("and the service is unavailable", func() {
			BeforeEach(func() {
				mocks.serviceManager.On("GetServiceAddresses", mock.Anything, projectName, data.serviceName).
					Return(nil, aiven.Error{
						Message:  "aiven-error",
						MoreInfo: "aiven-more-info",
						Status:   500,
					})
			})

			It("sets the correct aiven fail condition", func() {
				_, err := redisHandler.Apply(ctx, &application, logger)
				Expect(err).ToNot(Succeed())
				Expect(err).To(MatchError("operation GetService failed in Aiven: 500: aiven-error - aiven-more-info"))
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
			})
		})

		Context("and service users are unavailable", func() {
			BeforeEach(func() {
				defaultServiceManagerMock(data)
				mocks.serviceUserManager.On("Get", mock.Anything, data.username, projectName, data.serviceName, mock.Anything).
					Return(nil, aiven.Error{
						Message:  "aiven-error",
						MoreInfo: "aiven-more-info",
						Status:   500,
					})
			})

			It("sets the correct aiven fail condition", func() {
				_, err := redisHandler.Apply(ctx, &application, logger)
				Expect(err).ToNot(Succeed())
				Expect(err).To(MatchError("operation GetServiceUser failed in Aiven: 500: aiven-error - aiven-more-info"))
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
			})
		})
	})
})
