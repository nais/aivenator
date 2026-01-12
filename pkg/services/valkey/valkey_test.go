package valkey

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/aiven/project"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/utils"
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
	redisServiceURI          string
	serviceURI               string
	serviceHost              string
	servicePort              int
	replicaServiceURI        string
	replicaServiceHost       string
	replicaServicePort       int
	access                   string
	username                 string
	serviceNameAnnotationKey string
	serviceUserAnnotationKey string
	usernameKey              string
	passwordKey              string
	uriKey                   string
	hostKey                  string
	portKey                  string
	replicaUriKey            string
	replicaHostKey           string
	replicaPortKey           string
	redisUsernameKey         string
	redisPasswordKey         string
	redisUriKey              string
	redisHostKey             string
	redisPortKey             string
	secretName               string
}

var testInstances = []testData{
	{
		instanceName:             "my-instance1",
		serviceName:              "valkey-team-a-my-instance1",
		serviceURI:               "valkeys://my-instance1.example.com:23456",
		redisServiceURI:          "rediss://my-instance1.example.com:23456",
		serviceHost:              "my-instance1.example.com",
		servicePort:              23456,
		access:                   "read",
		username:                 "test-app-r-3D_",
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
		secretName:               "secret-1",
	},
	{
		instanceName:             "session-store",
		serviceName:              "valkey-team-a-session-store",
		serviceURI:               "valkeys://session-store.example.com:23456",
		redisServiceURI:          "rediss://session-store.example.com:23456",
		serviceHost:              "session-store.example.com",
		servicePort:              23456,
		access:                   "readwrite",
		username:                 "test-app-rw-3D_",
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
		secretName:               "secret-1",
	},
	{
		instanceName:             "with-replica",
		serviceName:              "valkey-team-a-with-replica",
		redisServiceURI:          "rediss://with-replica.example.com:23456",
		serviceURI:               "valkeys://with-replica.example.com:23456",
		serviceHost:              "with-replica.example.com",
		servicePort:              23456,
		replicaServiceURI:        "valkeys://replica-with-replica.example.com:23456",
		replicaServiceHost:       "replica-with-replica.example.com",
		replicaServicePort:       23456,
		access:                   "readwrite",
		username:                 "test-app-rw-3D_",
		serviceUserAnnotationKey: "with-replica.valkey.aiven.nais.io/serviceUser",
		serviceNameAnnotationKey: "with-replica.valkey.aiven.nais.io/serviceName",
		usernameKey:              "VALKEY_USERNAME_WITH_REPLICA",
		passwordKey:              "VALKEY_PASSWORD_WITH_REPLICA",
		uriKey:                   "VALKEY_URI_WITH_REPLICA",
		hostKey:                  "VALKEY_HOST_WITH_REPLICA",
		portKey:                  "VALKEY_PORT_WITH_REPLICA",
		replicaUriKey:            "VALKEY_REPLICA_URI_WITH_REPLICA",
		replicaHostKey:           "VALKEY_REPLICA_HOST_WITH_REPLICA",
		replicaPortKey:           "VALKEY_REPLICA_PORT_WITH_REPLICA",
		redisUriKey:              "REDIS_URI_WITH_REPLICA",
		redisPortKey:             "REDIS_PORT_WITH_REPLICA",
		redisHostKey:             "REDIS_HOST_WITH_REPLICA",
		redisPasswordKey:         "REDIS_PASSWORD_WITH_REPLICA",
		redisUsernameKey:         "REDIS_USERNAME_WITH_REPLICA",
		secretName:               "secret-1",
	},
}

var incompleteAccessControl = aiven.AccessControl{
	ValkeyACLCategories: []string{"-@all", "+@connection", "+@scripting", "+@pubsub", "+@transaction"},
	ValkeyACLKeys:       []string{"*"},
	ValkeyACLChannels:   []string{"*"},
}

type mockContainer struct {
	serviceUserManager *serviceuser.MockServiceUserManager
	serviceManager     *service.MockServiceManager
	projectManager     *project.MockProjectManager
}

func TestValkey(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Valkey Suite")
}

var _ = Describe("valkey.SecretConfig", func() {
	var logger log.FieldLogger
	var applicationBuilder aiven_nais_io_v1.AivenApplicationBuilder
	var application aiven_nais_io_v1.AivenApplication
	var valkeyHandler ValkeyHandler
	var mocks mockContainer
	var ctx context.Context
	var cancel context.CancelFunc

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

	defaultServiceManagerMock := func(data testData) {
		m := service.MockServiceAddresses{}
		m.EXPECT().Valkey().Return(service.ServiceAddress{
			URI:  data.serviceURI,
			Host: data.serviceHost,
			Port: data.servicePort,
		})
		if data.replicaServicePort != 0 {
			m.EXPECT().ValkeyReplica().Return(service.ServiceAddress{
				URI:  data.replicaServiceURI,
				Host: data.replicaServiceHost,
				Port: data.replicaServicePort,
			})
		} else {
			m.EXPECT().ValkeyReplica().Return(service.ServiceAddress{})
		}

		mocks.serviceManager.On("GetServiceAddresses", mock.Anything, projectName, data.serviceName).
			Return(&m, nil)
	}

	defaultAccessControl := func(data testData) *aiven.AccessControl {
		return &aiven.AccessControl{
			ValkeyACLCategories: getValkeyACLCategories(data.access),
			ValkeyACLCommands:   []string{"+info", "+cluster slots"},
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
			projectManager:     project.NewMockProjectManager(GinkgoT()),
		}
		valkeyHandler = ValkeyHandler{
			serviceuser: mocks.serviceUserManager,
			service:     mocks.serviceManager,
			projectName: projectName,
			secretConfig: utils.SecretConfig{
				Project:     mocks.projectManager,
				ProjectName: projectName,
			},
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
			individualSecrets, err := valkeyHandler.Apply(ctx, &application, logger)
			Expect(err).To(Succeed())
			Expect(individualSecrets).To(BeNil())
		})
	})

	When("it receives a spec with Valkey requested", func() {
		data := testInstances[0]

		BeforeEach(func() {
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Valkey: []*aiven_nais_io_v1.ValkeySpec{
						{
							Instance:   data.instanceName,
							Access:     data.access,
							SecretName: data.secretName,
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
				mocks.projectManager.On("GetCA", mock.Anything, projectName).
					Return("my-ca", nil)

			})

			It("sets the correct aiven fail condition", func() {
				individualSecrets, err := valkeyHandler.Apply(ctx, &application, logger)
				Expect(err).ToNot(Succeed())
				Expect(err).To(MatchError("operation GetService failed in Aiven: 500: aiven-error - aiven-more-info"))
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
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
				mocks.projectManager.On("GetCA", mock.Anything, projectName).
					Return("my-ca", nil)

			})

			It("sets the correct aiven fail condition", func() {
				individualSecrets, err := valkeyHandler.Apply(ctx, &application, logger)
				Expect(err).ToNot(Succeed())
				Expect(err).To(MatchError("operation GetServiceUser failed in Aiven: 500: aiven-error - aiven-more-info"))
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
			})
		})
	})

	When("it receives a spec", func() {
		data := testInstances[0]

		BeforeEach(func() {
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Valkey: []*aiven_nais_io_v1.ValkeySpec{
						{
							Instance:   data.instanceName,
							Access:     data.access,
							SecretName: data.secretName,
						},
					},
				}).
				Build()
		})

		Context("and the service user already exists", func() {
			Context("but with incomplete ACLs", func() {
				BeforeEach(func() {
					defaultServiceManagerMock(data)
					mocks.serviceUserManager.On("Get", mock.Anything, data.username, projectName, data.serviceName, mock.Anything).
						Return(&aiven.ServiceUser{
							Username:      data.username,
							Password:      servicePassword,
							AccessControl: incompleteAccessControl,
						}, nil)
					mocks.serviceUserManager.On("Update", mock.Anything, data.username, projectName, data.serviceName, defaultAccessControl(data), mock.Anything).
						Return(&aiven.ServiceUser{
							Username:      data.username,
							Password:      servicePassword,
							AccessControl: *defaultAccessControl(data),
						}, nil)
					mocks.projectManager.On("GetCA", mock.Anything, projectName).
						Return("my-ca", nil)

				})

				It("updates the existing user", func() {
					individualSecrets, err := valkeyHandler.Apply(ctx, &application, logger)
					Expect(individualSecrets).To(Not(BeNil()))
					Expect(err).To(Succeed())
				})
			})

			Context("with complete ACLs", func() {
				BeforeEach(func() {
					defaultServiceManagerMock(data)
					mocks.serviceUserManager.On("Get", mock.Anything, data.username, projectName, data.serviceName, mock.Anything).
						Return(&aiven.ServiceUser{
							Username:      data.username,
							Password:      servicePassword,
							AccessControl: *defaultAccessControl(data),
						}, nil)
					mocks.projectManager.On("GetCA", mock.Anything, projectName).
						Return("my-ca", nil)

				})

				It("uses the existing user", func() {
					individualSecrets, err := valkeyHandler.Apply(ctx, &application, logger)
					Expect(individualSecrets).To(Not(BeNil()))
					Expect(err).To(Succeed())
				})
			})
		})

		Context("and the service user doesn't exist", func() {
			BeforeEach(func() {
				defaultServiceManagerMock(data)
				mocks.serviceUserManager.On("Get", mock.Anything, data.username, projectName, data.serviceName, mock.Anything).
					Return(nil, aiven.Error{
						Message: "Service user does not exist",
						Status:  404,
					})
				mocks.serviceUserManager.On("Create", mock.Anything, data.username, projectName, data.serviceName, defaultAccessControl(data), mock.Anything).
					Return(&aiven.ServiceUser{
						Username:      data.username,
						Password:      servicePassword,
						AccessControl: *defaultAccessControl(data),
					}, nil)
				mocks.projectManager.On("GetCA", mock.Anything, projectName).
					Return("my-ca", nil)

			})

			It("creates the new user and returns credentials for the new user", func() {
				individualSecrets, err := valkeyHandler.Apply(ctx, &application, logger)
				Expect(err).To(Succeed())
				Expect(individualSecrets).To(Not(BeNil()))
			})
		})
	})
	When("it receives a spec with multiple newstyle instances", func() {
		BeforeEach(func() {
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Valkey: []*aiven_nais_io_v1.ValkeySpec{
						{
							Instance:   "my-instance1",
							Access:     "read",
							SecretName: "first-secret",
						}, {
							Instance:   "session-store",
							Access:     "readwrite",
							SecretName: "second-secret",
						}, {
							Instance:   "with-replica",
							Access:     "readwrite",
							SecretName: "replica-secret",
						},
					},
				}).
				Build()
		})

		Context("and the service user already exists", func() {
			BeforeEach(func() {
				for _, data := range testInstances {
					defaultServiceManagerMock(data)
					mocks.serviceUserManager.On("Get", mock.Anything, data.username, projectName, data.serviceName, mock.Anything).
						Return(&aiven.ServiceUser{
							Username:      data.username,
							Password:      servicePassword,
							AccessControl: *defaultAccessControl(data),
						}, nil)
					mocks.projectManager.On("GetCA", mock.Anything, projectName).
						Return("my-ca", nil).Once()

				}
			})

			It("uses the existing user", func() {
				individualSecrets, err := valkeyHandler.Apply(ctx, &application, logger)
				for i, data := range testInstances {
					assertHappy(&individualSecrets[i], data, err)
				}
				Expect(len(individualSecrets)).To(Equal(3))
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
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Valkey: specs,
				}).
				Build()
		})

		Context("and the service user already exists", func() {
			BeforeEach(func() {
				for _, data := range testInstances {
					defaultServiceManagerMock(data)
					mocks.serviceUserManager.On("Get", mock.Anything, data.username, projectName, data.serviceName, mock.Anything).
						Return(&aiven.ServiceUser{
							Username:      data.username,
							Password:      servicePassword,
							AccessControl: *defaultAccessControl(data),
						}, nil)
					mocks.projectManager.On("GetCA", mock.Anything, projectName).
						Return("my-ca", nil)
					mocks.projectManager.On("GetCA", mock.Anything, projectName).
						Return("my-ca", nil)

				}
			})

			It("uses the existing user", func() {
				individualSecrets, err := valkeyHandler.Apply(ctx, &application, logger)
				Expect(individualSecrets).To(Not(BeNil()))
				for index, data := range testInstances {
					assertHappy(&individualSecrets[index], data, err)
				}
			})
		})

		Context("and the service user doesn't exist", func() {
			BeforeEach(func() {
				for _, data := range testInstances {
					defaultServiceManagerMock(data)
					mocks.serviceUserManager.On("Get", mock.Anything, data.username, projectName, data.serviceName, mock.Anything).
						Return(nil, aiven.Error{
							Message:  "aiven-error",
							MoreInfo: "aiven-more-info",
							Status:   404,
						})
					mocks.serviceUserManager.On("Create", mock.Anything, data.username, projectName, data.serviceName, defaultAccessControl(data), mock.Anything).
						Return(&aiven.ServiceUser{
							Username:      data.username,
							Password:      servicePassword,
							AccessControl: *defaultAccessControl(data),
						}, nil)
				}
				mocks.projectManager.On("GetCA", mock.Anything, projectName).
					Return("my-ca", nil)

			})

			It("creates the new user and returns credentials for the new user", func() {
				makeKey := func(prefix, instanceName string) string {
					envVarSuffix := envVarName(instanceName)
					return fmt.Sprintf("%s_%s", prefix, envVarSuffix)
				}
				individualSecrets, err := valkeyHandler.Apply(ctx, &application, logger)
				Expect(err).To(Succeed())
				Expect(individualSecrets).To(Not(BeNil()))
				Expect(utils.KeysFromStringMap(individualSecrets[0].StringData)).To(ConsistOf(
					makeKey(ValkeyUser, testInstances[0].instanceName),
					makeKey(ValkeyPassword, testInstances[0].instanceName),
					makeKey(ValkeyURI, testInstances[0].instanceName),
					makeKey(ValkeyHost, testInstances[0].instanceName),
					makeKey(ValkeyPort, testInstances[0].instanceName),
					makeKey(RedisUser, testInstances[0].instanceName),
					makeKey(RedisPassword, testInstances[0].instanceName),
					makeKey(RedisURI, testInstances[0].instanceName),
					makeKey(RedisHost, testInstances[0].instanceName),
					makeKey(RedisPort, testInstances[0].instanceName),
					utils.AivenCAKey,
					utils.AivenSecretUpdatedKey,
				))
				Expect(utils.KeysFromStringMap(individualSecrets[2].StringData)).To(ConsistOf(
					makeKey(ValkeyUser, testInstances[2].instanceName),
					makeKey(ValkeyPassword, testInstances[2].instanceName),
					makeKey(ValkeyURI, testInstances[2].instanceName),
					makeKey(ValkeyHost, testInstances[2].instanceName),
					makeKey(ValkeyPort, testInstances[2].instanceName),
					makeKey(ValkeyReplicaURI, testInstances[2].instanceName),
					makeKey(ValkeyReplicaHost, testInstances[2].instanceName),
					makeKey(ValkeyReplicaPort, testInstances[2].instanceName),
					makeKey(RedisUser, testInstances[2].instanceName),
					makeKey(RedisPassword, testInstances[2].instanceName),
					makeKey(RedisURI, testInstances[2].instanceName),
					makeKey(RedisHost, testInstances[2].instanceName),
					makeKey(RedisPort, testInstances[2].instanceName),
					utils.AivenCAKey,
					utils.AivenSecretUpdatedKey,
				))
			})
		})
	})
})
