package valkey

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/aiven/aiven-go-client/v2"

	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/handlers/secret"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
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
	redisServiceURI          string
	serviceHost              string
	servicePort              int
	access                   string
	username                 string
	secretName               string
	serviceNameAnnotationKey string
	serviceUserAnnotationKey string
	usernameKey              string
	passwordKey              string
	uriKey                   string
	hostKey                  string
	portKey                  string
	redisUsernameKey         string
	redisPasswordKey         string
	redisUriKey              string
	redisHostKey             string
	redisPortKey             string
}

var testInstances = map[string]testData{
	"my-instance1": {
		instanceName:             "my-instance1",
		serviceName:              "valkey-team-a-my-instance1",
		serviceURI:               "valkeys://my-instance1.example.com:23456",
		redisServiceURI:          "rediss://my-instance1.example.com:23456",
		serviceHost:              "my-instance1.example.com",
		servicePort:              23456,
		access:                   "read",
		username:                 "test-app-r-9Nv",
		secretName:               "foo",
		serviceUserAnnotationKey: "my-instance1.valkey.aiven.nais.io/serviceUser",
		serviceNameAnnotationKey: "my-instance1.valkey.aiven.nais.io/serviceName",
		usernameKey:              "VALKEY_USERNAME_MY_INSTANCE1",
		passwordKey:              "VALKEY_PASSWORD_MY_INSTANCE1",
		uriKey:                   "VALKEY_URI_MY_INSTANCE1",
		hostKey:                  "VALKEY_HOST_MY_INSTANCE1",
		portKey:                  "VALKEY_PORT_MY_INSTANCE1",
		redisUriKey:              "REDIS_URI_MY_INSTANCE1",
		redisPortKey:             "REDIS_PORT_MY_INSTANCE1",
		redisHostKey:             "REDIS_HOST_MY_INSTANCE1",
		redisPasswordKey:         "REDIS_PASSWORD_MY_INSTANCE1",
		redisUsernameKey:         "REDIS_USERNAME_MY_INSTANCE1",
	},
	"session-store": {
		instanceName:             "session-store",
		serviceName:              "valkey-team-a-session-store",
		serviceURI:               "valkeys://session-store.example.com:23456",
		redisServiceURI:          "rediss://session-store.example.com:23456",
		serviceHost:              "session-store.example.com",
		servicePort:              23456,
		access:                   "readwrite",
		username:                 "test-app-rw-9Nv",
		secretName:               "bar",
		serviceUserAnnotationKey: "session-store.valkey.aiven.nais.io/serviceUser",
		serviceNameAnnotationKey: "session-store.valkey.aiven.nais.io/serviceName",
		usernameKey:              "VALKEY_USERNAME_SESSION_STORE",
		passwordKey:              "VALKEY_PASSWORD_SESSION_STORE",
		uriKey:                   "VALKEY_URI_SESSION_STORE",
		hostKey:                  "VALKEY_HOST_SESSION_STORE",
		portKey:                  "VALKEY_PORT_SESSION_STORE",
		redisUriKey:              "REDIS_URI_SESSION_STORE",
		redisPortKey:             "REDIS_PORT_SESSION_STORE",
		redisHostKey:             "REDIS_HOST_SESSION_STORE",
		redisPasswordKey:         "REDIS_PASSWORD_SESSION_STORE",
		redisUsernameKey:         "REDIS_USERNAME_SESSION_STORE",
	},
}

type mockContainer struct {
	serviceUserManager *serviceuser.MockServiceUserManager
	serviceManager     *service.MockServiceManager
	secretsHandler     *secret.MockSecrets
}

func TestValkey(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Valkey Suite")
}

var _ = Describe("valkey.Handler", func() {
	var logger log.FieldLogger
	var applicationBuilder aiven_nais_io_v1.AivenApplicationBuilder
	var application aiven_nais_io_v1.AivenApplication
	var valkeyHandler ValkeyHandler
	var mocks mockContainer
	var ctx context.Context
	var cancel context.CancelFunc

	defaultServiceManagerMock := func(data testData) {
		mocks.serviceManager.On("GetServiceAddresses", mock.Anything, projectName, data.serviceName).
			Return(&service.ServiceAddresses{
				Valkey: service.ServiceAddress{
					URI:  data.serviceURI,
					Host: data.serviceHost,
					Port: data.servicePort,
				},
			}, nil)
	}
	defaultAccessControl := func(data testData) *aiven.AccessControl {
		return &aiven.AccessControl{
			ValkeyACLCategories: getValkeyACLCategories(data.access),
			ValkeyACLKeys:       []string{"*"},
			ValkeyACLChannels:   []string{"*"},
		}
	}

	BeforeEach(func() {
		root := log.New()
		root.Out = GinkgoWriter
		logger = log.NewEntry(root)
		applicationBuilder = aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace)
		mocks = mockContainer{
			serviceUserManager: serviceuser.NewMockServiceUserManager(GinkgoT()),
			serviceManager:     service.NewMockServiceManager(GinkgoT()),
			secretsHandler:     secret.NewMockSecrets(GinkgoT()),
		}

		valkeyHandler = ValkeyHandler{
			serviceuser:    mocks.serviceUserManager,
			service:        mocks.serviceManager,
			projectName:    projectName,
			secretsHandler: mocks.secretsHandler,
		}
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	})

	AfterEach(func() {
		cancel()
	})
	When("it receives a spec without Valkey", func() {
		BeforeEach(func() {
			application = applicationBuilder.Build()
		})

		It("ignores it", func() {
			var typedNil []*corev1.Secret

			Expect(true).To(Equal(true))
			result, err := valkeyHandler.Apply(ctx, &application, logger)
			Expect(err).To(Succeed())
			Expect(result).To(Equal(typedNil))
		})
	})

	When("it receives a spec with Valkey requested", func() {
		data := testInstances["my-instance1"]

		BeforeEach(func() {
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Valkey: []*aiven_nais_io_v1.ValkeySpec{
						{
							Instance:   testInstances["my-instance1"].instanceName,
							Access:     testInstances["my-instance1"].access,
							SecretName: testInstances["my-instance1"].secretName,
						},
						{
							Instance: testInstances["session-store"].instanceName,
							Access:   testInstances["session-store"].access,
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
				_, err := valkeyHandler.Apply(ctx, &application, logger)
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
				mocks.secretsHandler.On("GetOrInitSecret", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(corev1.Secret{})
			})

			It("sets the correct aiven fail condition", func() {
				_, err := valkeyHandler.Apply(ctx, &application, logger)
				Expect(err).ToNot(Succeed())
				Expect(err).To(MatchError("operation GetServiceUser failed in Aiven: 500: aiven-error - aiven-more-info"))
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
			})
		})
	})

	When("it receives a spec", func() {
		data := testInstances

		BeforeEach(func() {
			mocks.secretsHandler.On("GetOrInitSecret", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(corev1.Secret{})

			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Valkey: []*aiven_nais_io_v1.ValkeySpec{
						{
							Instance:   data["my-instance1"].instanceName,
							Access:     data["my-instance1"].access,
							SecretName: "foo",
						},
					},
				}).
				Build()
		})

		assertHappy := func(secret *corev1.Secret, data testData, err error) {
			GinkgoHelper()
			Expect(err).To(Succeed())
			Expect(validation.ValidateAnnotations(secret.GetAnnotations(), field.NewPath("metadata.annotations"))).To(BeEmpty())
			Expect(secret.GetAnnotations()).To(HaveKeyWithValue(ProjectAnnotation, projectName))
			Expect(secret.GetAnnotations()).To(HaveKeyWithValue(data.serviceUserAnnotationKey, data.username))
			Expect(secret.GetAnnotations()).To(HaveKeyWithValue(data.serviceNameAnnotationKey, data.serviceName))
			Expect(secret.StringData).To(HaveKeyWithValue(data.usernameKey, data.username))
			Expect(secret.StringData).To(HaveKeyWithValue(data.passwordKey, servicePassword))
			Expect(secret.StringData).To(HaveKeyWithValue(data.uriKey, data.serviceURI))
			Expect(secret.StringData).To(HaveKeyWithValue(data.hostKey, data.serviceHost))
			Expect(secret.StringData).To(HaveKeyWithValue(data.portKey, strconv.Itoa(data.servicePort)))
			Expect(secret.StringData).To(HaveKeyWithValue(data.redisUsernameKey, data.username))
			Expect(secret.StringData).To(HaveKeyWithValue(data.redisPasswordKey, servicePassword))
			Expect(secret.StringData).To(HaveKeyWithValue(data.redisUriKey, data.redisServiceURI))
			Expect(secret.StringData).To(HaveKeyWithValue(data.redisHostKey, data.serviceHost))
			Expect(secret.StringData).To(HaveKeyWithValue(data.redisPortKey, strconv.Itoa(data.servicePort)))
		}

		Context("and the service user already exists", func() {
			BeforeEach(func() {
				defaultServiceManagerMock(data["my-instance1"])
				mocks.serviceUserManager.On("Get", mock.Anything, data["my-instance1"].username, projectName, data["my-instance1"].serviceName, mock.Anything).
					Return(&aiven.ServiceUser{
						Username: data["my-instance1"].username,
						Password: servicePassword,
					}, nil)
			})

			It("uses the existing user", func() {
				secrets, err := valkeyHandler.Apply(ctx, &application, logger)
				for _, secret := range secrets {
					assertHappy(secret, testInstances[secret.Annotations[InstanceAnnotation]], err)
				}
			})
		})

		Context("and the service user doesn't exist", func() {
			BeforeEach(func() {
				defaultServiceManagerMock(data["my-instance1"])
				mocks.secretsHandler.On("GetOrInitSecret", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(corev1.Secret{})

				mocks.serviceUserManager.On("Get", mock.Anything, data["my-instance1"].username, projectName, data["my-instance1"].serviceName, mock.Anything).
					Return(nil, aiven.Error{
						Message: "Service user does not exist",
						Status:  404,
					})
				mocks.serviceUserManager.On("Create", mock.Anything, data["my-instance1"].username, projectName, data["my-instance1"].serviceName, defaultAccessControl(data["my-instance1"]), mock.Anything).
					Return(&aiven.ServiceUser{
						Username: data["my-instance1"].username,
						Password: servicePassword,
					}, nil)
			})

			It("creates the new user and returns credentials for the new user", func() {
				secrets, err := valkeyHandler.Apply(ctx, &application, logger)
				for _, secret := range secrets {
					assertHappy(secret, testInstances[secret.Annotations[InstanceAnnotation]], err)
				}
			})
		})
	})

	When("it receives a spec with multiple instances", func() {
		BeforeEach(func() {
			var specs []*aiven_nais_io_v1.ValkeySpec
			for _, data := range testInstances {
				specs = append(specs, &aiven_nais_io_v1.ValkeySpec{
					Instance:   data.instanceName,
					Access:     data.access,
					SecretName: data.secretName,
				})
			}
			mocks.secretsHandler.On("GetOrInitSecret", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(corev1.Secret{})

			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Valkey: specs,
				}).
				Build()
		})

		assertHappy := func(secret *corev1.Secret, data testData, err error) {
			GinkgoHelper()
			Expect(err).To(Succeed())
			Expect(validation.ValidateAnnotations(secret.GetAnnotations(), field.NewPath("metadata.annotations"))).To(BeEmpty())
			Expect(secret.GetAnnotations()).To(HaveKeyWithValue(ProjectAnnotation, projectName))
			Expect(secret.GetAnnotations()).To(HaveKeyWithValue(data.serviceUserAnnotationKey, data.username))
			Expect(secret.GetAnnotations()).To(HaveKeyWithValue(data.serviceNameAnnotationKey, data.serviceName))
			Expect(secret.StringData).To(HaveKeyWithValue(data.usernameKey, data.username))
			Expect(secret.StringData).To(HaveKeyWithValue(data.passwordKey, servicePassword))

			Expect(secret.StringData).To(HaveKeyWithValue(data.uriKey, data.serviceURI))
			Expect(secret.StringData).To(HaveKeyWithValue(data.hostKey, data.serviceHost))
			Expect(secret.StringData).To(HaveKeyWithValue(data.portKey, strconv.Itoa(data.servicePort)))
		}

		Context("and the service user already exists", func() {
			BeforeEach(func() {
				for _, data := range testInstances {
					defaultServiceManagerMock(data)
					mocks.secretsHandler.On("GetOrInitSecret", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(corev1.Secret{})

					mocks.serviceUserManager.On("Get", mock.Anything, data.username, projectName, data.serviceName, mock.Anything).
						Return(&aiven.ServiceUser{
							Username: data.username,
							Password: servicePassword,
						}, nil)
				}
			})

			It("uses the existing user", func() {
				secrets, err := valkeyHandler.Apply(ctx, &application, logger)
				for _, secret := range secrets {
					assertHappy(secret, testInstances[secret.Annotations[InstanceAnnotation]], err)
				}
			})
		})

		Context("and the service user doesn't exist", func() {
			BeforeEach(func() {
				for _, data := range testInstances {
					defaultServiceManagerMock(data)
					mocks.secretsHandler.On("GetOrInitSecret", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(corev1.Secret{})

					mocks.serviceUserManager.On("Get", mock.Anything, data.username, projectName, data.serviceName, mock.Anything).
						Return(nil, aiven.Error{
							Message:  "aiven-error",
							MoreInfo: "aiven-more-info",
							Status:   404,
						})
					mocks.serviceUserManager.On("Create", mock.Anything, data.username, projectName, data.serviceName, defaultAccessControl(data), mock.Anything).
						Return(&aiven.ServiceUser{
							Username: data.username,
							Password: servicePassword,
						}, nil)
				}
			})

			It("creates the new user and returns credentials for the new user", func() {
				secrets, err := valkeyHandler.Apply(ctx, &application, logger)
				for _, secret := range secrets {
					assertHappy(secret, testInstances[secret.Annotations[InstanceAnnotation]], err)
				}
			})
		})
	})
})
