package valkey

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiven/aiven-go-client/v2"
	operator "github.com/nais/aivenator/pkg/aiven/operator"
	"github.com/nais/aivenator/pkg/aiven/project"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/utils"
	aiven_io_v1alpha1 "github.com/nais/liberator/pkg/apis/aiven.io/v1alpha1"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
	legacyUsername           string
	serviceNameAnnotationKey string
	serviceUserAnnotationKey string
	legacyAnnotationKey      string
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

// legacyUsername values deliberately use the pre-CR base64 suffix alphabet
// (uppercase, '_'): such names are invalid as CR names, so Apply must mint a
// fresh username and track the legacy one instead of adopting it.
var testInstances = []testData{
	{
		instanceName:             "my-instance1",
		serviceName:              "valkey-team-a-my-instance1",
		serviceURI:               "valkeys://my-instance1.example.com:23456",
		redisServiceURI:          "rediss://my-instance1.example.com:23456",
		serviceHost:              "my-instance1.example.com",
		servicePort:              23456,
		access:                   "read",
		legacyUsername:           "test-app-r-3D_",
		serviceUserAnnotationKey: "my-instance1.valkey.aiven.nais.io/serviceUser",
		serviceNameAnnotationKey: "my-instance1.valkey.aiven.nais.io/serviceName",
		legacyAnnotationKey:      "my-instance1.valkey.aiven.nais.io/legacyServiceUser",
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
		legacyUsername:           "test-app-rw-3D_",
		serviceUserAnnotationKey: "session-store.valkey.aiven.nais.io/serviceUser",
		serviceNameAnnotationKey: "session-store.valkey.aiven.nais.io/serviceName",
		legacyAnnotationKey:      "session-store.valkey.aiven.nais.io/legacyServiceUser",
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
		legacyUsername:           "test-app-rw-3D_",
		serviceUserAnnotationKey: "with-replica.valkey.aiven.nais.io/serviceUser",
		serviceNameAnnotationKey: "with-replica.valkey.aiven.nais.io/serviceName",
		legacyAnnotationKey:      "with-replica.valkey.aiven.nais.io/legacyServiceUser",
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

// legacyValkeySecret builds an app-facing secret carrying a pre-CR (old mode)
// serviceUser annotation for each given instance.
func legacyValkeySecret(name string, instances ...testData) *corev1.Secret {
	annotations := map[string]string{}
	for _, d := range instances {
		annotations[d.serviceUserAnnotationKey] = d.legacyUsername
	}
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Annotations: annotations}}
}

type mockContainer struct {
	serviceUserManager *serviceuser.MockServiceUserManager
	crServiceUser      *operator.MockServiceUserManager
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

	// expectedUsername computes the CR-mode username Apply mints for the built
	// application: package logic reused on purpose, so the tests assert the
	// wiring, not the formula.
	expectedUsername := func(data testData) string {
		GinkgoHelper()
		return utils.ServiceUserName(appName, data.access, data.instanceName, data.secretName, time.Now())
	}

	assertHappy := func(secret *corev1.Secret, data testData, username string, err error) {
		GinkgoHelper()
		Expect(err).To(Succeed())
		Expect(validation.ValidateAnnotations(secret.GetAnnotations(), field.NewPath("metadata.annotations"))).To(BeEmpty())
		Expect(secret.GetAnnotations()).To(HaveKeyWithValue(ProjectAnnotation, projectName))
		Expect(secret.GetAnnotations()).To(HaveKeyWithValue(data.serviceUserAnnotationKey, username))
		Expect(secret.GetAnnotations()).To(HaveKeyWithValue(data.serviceNameAnnotationKey, data.serviceName))
		Expect(secret.StringData).To(HaveKeyWithValue(data.usernameKey, username))
		Expect(secret.StringData).To(HaveKeyWithValue(data.passwordKey, servicePassword))
		Expect(secret.StringData).To(HaveKeyWithValue(data.uriKey, data.serviceURI))
		Expect(secret.StringData).To(HaveKeyWithValue(data.hostKey, data.serviceHost))
		Expect(secret.StringData).To(HaveKeyWithValue(data.portKey, strconv.Itoa(data.servicePort)))
		Expect(secret.StringData).To(HaveKeyWithValue(data.redisUsernameKey, username))
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
		}).Maybe()
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

	// defaultCRServiceUserMock expects a ServiceUser CR for username on the
	// instance's service and publishes the connection details Apply reprojects.
	defaultCRServiceUserMock := func(data testData, username string) {
		mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.MatchedBy(func(spec operator.ServiceUserSpec) bool {
			return spec.Name == username && spec.ServiceName == data.serviceName && spec.Namespace == namespace && spec.AccessControl != nil
		}), mock.Anything).
			Return(&operator.ServiceUser{
				Username: username,
				Secret: map[string]string{
					operator.ServiceUserUsername: username,
					operator.ServiceUserPassword: servicePassword,
					operator.ServiceUserHost:     data.serviceHost,
					operator.ServiceUserPort:     strconv.Itoa(data.servicePort),
				},
			}, nil)
	}

	BeforeEach(func() {
		root := log.New()
		root.Out = GinkgoWriter
		logger = log.NewEntry(root)
		applicationBuilder = aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace)
		mocks = mockContainer{
			serviceUserManager: serviceuser.NewMockServiceUserManager(GinkgoT()),
			crServiceUser:      operator.NewMockServiceUserManager(GinkgoT()),
			serviceManager:     service.NewMockServiceManager(GinkgoT()),
			projectManager:     project.NewMockProjectManager(GinkgoT()),
		}

		scheme := runtime.NewScheme()
		Expect(aiven_io_v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		// Pre-populate Valkey CRs matching testInstances in namespace "team-a".
		// The seeded app-facing secrets carry legacy (pre-CR, RFC-1123-invalid)
		// usernames, so the existing specs exercise the legacy-tracking flow.
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&aiven_io_v1alpha1.Valkey{ObjectMeta: metav1.ObjectMeta{Name: "valkey-team-a-my-instance1", Namespace: namespace}, Status: aiven_io_v1alpha1.ValkeyStatus{State: utils.ReadyState}},
			&aiven_io_v1alpha1.Valkey{ObjectMeta: metav1.ObjectMeta{Name: "valkey-team-a-session-store", Namespace: namespace}, Status: aiven_io_v1alpha1.ValkeyStatus{State: utils.ReadyState}},
			&aiven_io_v1alpha1.Valkey{ObjectMeta: metav1.ObjectMeta{Name: "valkey-team-a-with-replica", Namespace: namespace}, Status: aiven_io_v1alpha1.ValkeyStatus{State: utils.ReadyState}},
			legacyValkeySecret("secret-1", testInstances[0], testInstances[1], testInstances[2]),
			legacyValkeySecret("first-secret", testInstances[0]),
			legacyValkeySecret("second-secret", testInstances[1]),
			legacyValkeySecret("replica-secret", testInstances[2]),
		).Build()

		valkeyHandler = ValkeyHandler{
			serviceuser:   mocks.serviceUserManager,
			crServiceUser: mocks.crServiceUser,
			service:       mocks.serviceManager,
			projectName:   projectName,
			secretConfig: utils.SecretConfig{
				Project:     mocks.projectManager,
				ProjectName: projectName,
			},
			k8sReader: fakeClient,
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

		Context("and the service user cannot be provisioned", func() {
			BeforeEach(func() {
				defaultServiceManagerMock(data)
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("failed to provision"))
				mocks.projectManager.On("GetCA", mock.Anything, projectName).
					Return("my-ca", nil)
			})

			It("sets the correct aiven fail condition", func() {
				individualSecrets, err := valkeyHandler.Apply(ctx, &application, logger)
				Expect(err).ToNot(Succeed())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
			})
		})

		// The operator publishes the ServiceUser secret; a missing key must fail
		// loudly rather than project an empty credential into the app secret.
		Context("and the operator secret is missing a required key", func() {
			BeforeEach(func() {
				defaultServiceManagerMock(data)
				mocks.projectManager.On("GetCA", mock.Anything, projectName).Return("my-ca", nil)
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(&operator.ServiceUser{
						Username: "minted",
						Secret:   map[string]string{operator.ServiceUserHost: data.serviceHost, operator.ServiceUserPort: strconv.Itoa(data.servicePort)},
					}, nil)
			})
			It("sets the correct aiven fail condition", func() {
				individualSecrets, err := valkeyHandler.Apply(ctx, &application, logger)
				Expect(err).ToNot(Succeed())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
			})
		})

		Context("and the project CA cannot be fetched", func() {
			BeforeEach(func() {
				mocks.projectManager.On("GetCA", mock.Anything, projectName).Return("", aiven.Error{Message: "boom", Status: 500})
			})
			It("sets the correct aiven fail condition", func() {
				individualSecrets, err := valkeyHandler.Apply(ctx, &application, logger)
				Expect(err).ToNot(Succeed())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
			})
		})
	})

	When("the app-facing secret carries a legacy (pre-CR) username", func() {
		data := testInstances[0]

		BeforeEach(func() {
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Valkey: []*aiven_nais_io_v1.ValkeySpec{
						{Instance: data.instanceName, Access: data.access, SecretName: data.secretName},
					},
				}).
				Build()
			defaultServiceManagerMock(data)
			defaultCRServiceUserMock(data, expectedUsername(data))
			mocks.projectManager.On("GetCA", mock.Anything, projectName).Return("my-ca", nil)
		})

		It("mints a fresh username and tracks the legacy one", func() {
			individualSecrets, err := valkeyHandler.Apply(ctx, &application, logger)
			Expect(err).To(Succeed())
			Expect(individualSecrets).To(HaveLen(1))
			assertHappy(&individualSecrets[0], data, expectedUsername(data), err)
			Expect(individualSecrets[0].GetAnnotations()).To(HaveKeyWithValue(data.legacyAnnotationKey, data.legacyUsername))
		})
	})

	When("the app-facing secret's username has a CR targeting this service", func() {
		data := testInstances[0]
		const adoptedUsername = "test-app-rw-5fc"

		BeforeEach(func() {
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Valkey: []*aiven_nais_io_v1.ValkeySpec{
						{Instance: data.instanceName, Access: data.access, SecretName: "adopted-secret"},
					},
				}).
				Build()
			existing := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: "adopted-secret", Namespace: namespace,
				Annotations: map[string]string{data.serviceUserAnnotationKey: adoptedUsername},
			}}
			Expect(valkeyHandler.k8sReader.(client.Client).Create(ctx, existing)).To(Succeed())

			mocks.crServiceUser.On("ServiceName", mock.Anything, namespace, adoptedUsername).Return(data.serviceName, true, nil)
			defaultServiceManagerMock(data)
			defaultCRServiceUserMock(data, adoptedUsername)
			mocks.projectManager.On("GetCA", mock.Anything, projectName).Return("my-ca", nil)
		})

		It("adopts the existing username", func() {
			individualSecrets, err := valkeyHandler.Apply(ctx, &application, logger)
			Expect(err).To(Succeed())
			Expect(individualSecrets).To(HaveLen(1))
			assertHappy(&individualSecrets[0], data, adoptedUsername, err)
			Expect(individualSecrets[0].GetAnnotations()).ToNot(HaveKey(data.legacyAnnotationKey))
		})
	})

	When("the app-facing secret's username has a CR targeting another instance's service", func() {
		data := testInstances[0]
		// A pre-per-instance name shared by the app's instances: its CR already
		// targets a sibling's service, and spec.serviceName is immutable, so this
		// instance must mint its own name rather than re-point the CR.
		const sharedUsername = "test-app-rw-5fc"
		var minted string

		BeforeEach(func() {
			minted = utils.ServiceUserName(appName, data.access, data.instanceName, "shared-secret", time.Now())
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Valkey: []*aiven_nais_io_v1.ValkeySpec{
						{Instance: data.instanceName, Access: data.access, SecretName: "shared-secret"},
					},
				}).
				Build()
			existing := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: "shared-secret", Namespace: namespace,
				Annotations: map[string]string{data.serviceUserAnnotationKey: sharedUsername},
			}}
			Expect(valkeyHandler.k8sReader.(client.Client).Create(ctx, existing)).To(Succeed())

			mocks.crServiceUser.On("ServiceName", mock.Anything, namespace, sharedUsername).Return("valkey-team-a-other-instance", true, nil)
			defaultServiceManagerMock(data)
			defaultCRServiceUserMock(data, minted)
			mocks.projectManager.On("GetCA", mock.Anything, projectName).Return("my-ca", nil)
		})

		It("mints a per-instance username instead of re-pointing the shared CR", func() {
			individualSecrets, err := valkeyHandler.Apply(ctx, &application, logger)
			Expect(err).To(Succeed())
			Expect(individualSecrets).To(HaveLen(1))
			Expect(minted).ToNot(Equal(sharedUsername))
			assertHappy(&individualSecrets[0], data, minted, err)
			Expect(individualSecrets[0].GetAnnotations()).ToNot(HaveKey(data.legacyAnnotationKey))
		})
	})

	When("the app-facing secret does not yet exist", func() {
		data := testInstances[0]

		BeforeEach(func() {
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Valkey: []*aiven_nais_io_v1.ValkeySpec{
						{Instance: data.instanceName, Access: data.access, SecretName: data.secretName},
					},
				}).
				Build()
			defaultServiceManagerMock(data)
			defaultCRServiceUserMock(data, expectedUsername(data))
			mocks.projectManager.On("GetCA", mock.Anything, projectName).Return("my-ca", nil)
		})

		It("provisions via the ServiceUser CR and marks the secret", func() {
			individualSecrets, err := valkeyHandler.Apply(ctx, &application, logger)
			Expect(err).To(Succeed())
			Expect(individualSecrets).To(HaveLen(1))
			Expect(individualSecrets[0].GetAnnotations()).To(HaveKeyWithValue(instanceAnnotation(data.instanceName, ServiceUserAnnotation), expectedUsername(data)))
			Expect(individualSecrets[0].StringData).To(HaveKeyWithValue(data.usernameKey, expectedUsername(data)))
			Expect(individualSecrets[0].StringData).To(HaveKeyWithValue(data.passwordKey, servicePassword))
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
			for _, data := range testInstances {
				defaultServiceManagerMock(data)
				defaultCRServiceUserMock(data, expectedUsername(data))
			}
			mocks.projectManager.On("GetCA", mock.Anything, projectName).Return("my-ca", nil)
		})

		It("provisions every instance and returns complete secrets", func() {
			makeKey := func(prefix, instanceName string) string {
				envVarSuffix := envVarName(instanceName)
				return fmt.Sprintf("%s_%s", prefix, envVarSuffix)
			}
			individualSecrets, err := valkeyHandler.Apply(ctx, &application, logger)
			Expect(err).To(Succeed())
			Expect(individualSecrets).To(HaveLen(3))
			for i, data := range testInstances {
				assertHappy(&individualSecrets[i], data, expectedUsername(data), err)
			}
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

	// Namespace naming collision: valkey-<ns>-<instance> is ambiguous.
	// e.g. namespace "team-a" + instance "b-cache" and namespace "team-a-b" + instance "cache"
	// both produce "valkey-team-a-b-cache". Aiven rejects the duplicate, so the
	// colliding CR exists in-cluster but never reaches RUNNING.
	When("Valkey CR exists in namespace but is NOT in RUNNING state (naming collision)", func() {
		BeforeEach(func() {
			cr := &aiven_io_v1alpha1.Valkey{
				ObjectMeta: metav1.ObjectMeta{Name: "valkey-team-a-collided", Namespace: namespace},
				Status:     aiven_io_v1alpha1.ValkeyStatus{State: "NOT_RUNNING"},
			}
			Expect(valkeyHandler.k8sReader.(client.Client).Create(ctx, cr)).To(Succeed())

			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Valkey: []*aiven_nais_io_v1.ValkeySpec{
						{
							Instance:   "collided",
							Access:     "read",
							SecretName: "collided-secret",
						},
					},
				}).
				Build()
		})

		It("rejects because the instance is not RUNNING", func() {
			_, err := valkeyHandler.Apply(ctx, &application, logger)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("expected RUNNING"))
		})
	})

	// Security: cross-namespace access is rejected.
	// The handler scopes its CR lookup to the requesting namespace only.
	// Aiven APIs are mocked to succeed so that if the namespace check is ever removed (regression), the test still catches it via the assertion.
	When("Apply is called without a matching Valkey CR in the requesting namespace", func() {
		var attackerApp aiven_nais_io_v1.AivenApplication

		BeforeEach(func() {
			attackerApp = aiven_nais_io_v1.NewAivenApplicationBuilder("evil-app", "attacker-ns").
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Valkey: []*aiven_nais_io_v1.ValkeySpec{
						{
							Instance:   "stolen-cache",
							Access:     "admin",
							SecretName: "stolen-creds",
						},
					},
				}).
				Build()
			// Mocked so that if namespace check regresses, Apply() would succeed and this test's assertion catches it.
			m := service.MockServiceAddresses{}
			m.On("Valkey").Return(service.ServiceAddress{
				URI:  "valkeys://stolen.example.com:23456",
				Host: "stolen.example.com",
				Port: 23456,
			}).Maybe()
			m.On("ValkeyReplica").Return(service.ServiceAddress{}).Maybe()
			mocks.serviceManager.On("GetServiceAddresses", mock.Anything, projectName, "valkey-attacker-ns-stolen-cache").
				Return(&m, nil).Maybe()
			mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return(&operator.ServiceUser{
					Username: "evil-app-stolen-cache-abc",
					Secret: map[string]string{
						operator.ServiceUserUsername: "evil-app-stolen-cache-abc",
						operator.ServiceUserPassword: servicePassword,
						operator.ServiceUserHost:     "stolen.example.com",
						operator.ServiceUserPort:     "23456",
					},
				}, nil).Maybe()
			mocks.projectManager.On("GetCA", mock.Anything, projectName).
				Return("my-ca", nil).Maybe()
		})

		It("returns an error because no ownership validation passes", func() {
			individualSecrets, err := valkeyHandler.Apply(ctx, &attackerApp, logger)
			Expect(err).To(HaveOccurred(), "Apply() should reject when no Valkey CR exists in namespace")
			Expect(individualSecrets).To(BeNil())
		})
	})

	When("Cleanup is called", func() {
		data := testInstances[0]
		const crUsername = "test-app-my-instance1-r-abc"

		makeSecret := func(annotations map[string]string) *corev1.Secret {
			base := map[string]string{
				data.serviceNameAnnotationKey: data.serviceName,
				data.serviceUserAnnotationKey: crUsername,
				ProjectAnnotation:             projectName,
			}
			return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: data.secretName, Namespace: namespace,
				Annotations: utils.MergeStringMap(base, annotations),
			}}
		}

		It("deletes the ServiceUser CR when the CR exists", func() {
			secret := makeSecret(nil)
			mocks.crServiceUser.On("Exists", mock.Anything, namespace, crUsername).Return(true, nil)
			mocks.crServiceUser.On("DeleteServiceUser", mock.Anything, namespace, crUsername, mock.Anything).Return(nil)

			Expect(valkeyHandler.Cleanup(ctx, secret, logger)).To(Succeed())
		})

		It("deletes the user via the direct API when no CR exists", func() {
			secret := makeSecret(nil)
			mocks.crServiceUser.On("Exists", mock.Anything, namespace, crUsername).Return(false, nil)
			mocks.serviceUserManager.On("Delete", mock.Anything, crUsername, projectName, data.serviceName, mock.Anything).Return(nil)

			Expect(valkeyHandler.Cleanup(ctx, secret, logger)).To(Succeed())
		})

		// A failed direct-API delete must surface so the finalizer is retained and
		// the drain retries, rather than silently leaking the Aiven user.
		It("returns the error when the direct-API delete fails", func() {
			secret := makeSecret(nil)
			mocks.crServiceUser.On("Exists", mock.Anything, namespace, crUsername).Return(false, nil)
			mocks.serviceUserManager.On("Delete", mock.Anything, crUsername, projectName, data.serviceName, mock.Anything).Return(aiven.Error{Message: "boom", Status: 500})

			Expect(valkeyHandler.Cleanup(ctx, secret, logger)).ToNot(Succeed())
		})

		It("also deletes a tracked legacy user via the direct API", func() {
			secret := makeSecret(map[string]string{
				data.legacyAnnotationKey: data.legacyUsername,
			})
			mocks.crServiceUser.On("Exists", mock.Anything, namespace, crUsername).Return(true, nil)
			mocks.crServiceUser.On("DeleteServiceUser", mock.Anything, namespace, crUsername, mock.Anything).Return(nil)
			mocks.serviceUserManager.On("Delete", mock.Anything, data.legacyUsername, projectName, data.serviceName, mock.Anything).Return(nil)

			Expect(valkeyHandler.Cleanup(ctx, secret, logger)).To(Succeed())
		})
	})
})

var _ = Describe("utils.ServiceUserName", func() {
	const nameShape = `^[a-z0-9][a-z0-9-]*-(a|rw|w|r)-[0-9a-f]{6}-[0-9a-f]{5}-[0-9]{4}w[0-9]{2}$`
	mintTime := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	// familyPrefix is <app>-<access>-<h1>: the name minus its trailing h2 and week.
	familyPrefix := func(name string) string {
		segs := strings.Split(name, "-")
		return strings.Join(segs[:len(segs)-2], "-")
	}

	It("matches the target scheme and encodes the access level", func() {
		for access, code := range map[string]string{"admin": "a", "readwrite": "rw", "write": "w", "read": "r", "": "r", "bogus": "r"} {
			name := utils.ServiceUserName("my-api", access, "my-instance", "my-secret", mintTime)
			Expect(name).To(MatchRegexp(nameShape), "access %q", access)
			Expect(name).To(HavePrefix("my-api-"+code+"-"), "access %q should map to code %q", access, code)
		}
	})

	It("is always a valid CR name", func() {
		accesses := []string{"admin", "readwrite", "write", "read", ""}
		for i := range 500 {
			name := utils.ServiceUserName(fmt.Sprintf("app-%d", i), accesses[i%len(accesses)], fmt.Sprintf("instance-%d", i), fmt.Sprintf("secret-%d", i), mintTime.AddDate(0, 0, i))
			Expect(utils.IsValidCRName(name)).To(BeTrue(), "not a valid CR name: %q", name)
		}
	})

	It("caps pathological names at Aiven's 64-char limit", func() {
		name := utils.ServiceUserName("an-application-with-a-very-long-name-close-to-k8s-limits-yes", "readwrite", "an-equally-long-instance-name-that-blows-the-budget", "a-very-long-secret-name-too", mintTime)
		Expect(len(name)).To(BeNumerically("<=", aiven_nais_io_v1.MaxServiceUserNameLength))
		Expect(utils.IsValidCRName(name)).To(BeTrue())
		Expect(name).To(MatchRegexp(nameShape))
	})

	It("keeps the family prefix stable across secretName and week", func() {
		base := utils.ServiceUserName("my-api", "readwrite", "my-instance", "secret-a", mintTime)
		otherSecret := utils.ServiceUserName("my-api", "readwrite", "my-instance", "secret-b", mintTime)
		otherWeek := utils.ServiceUserName("my-api", "readwrite", "my-instance", "secret-a", mintTime.AddDate(0, 0, 21))
		Expect(familyPrefix(base)).To(Equal(familyPrefix(otherSecret)))
		Expect(familyPrefix(base)).To(Equal(familyPrefix(otherWeek)))
	})

	It("gives distinct secretNames distinct usernames", func() {
		a := utils.ServiceUserName("my-api", "read", "my-instance", "secret-a", mintTime)
		b := utils.ServiceUserName("my-api", "read", "my-instance", "secret-b", mintTime)
		Expect(a).ToNot(Equal(b))
	})

	It("ends with the ISO week-year and week of its mint time", func() {
		year, week := mintTime.ISOWeek()
		Expect(utils.ServiceUserName("my-api", "read", "my-instance", "my-secret", mintTime)).To(HaveSuffix(fmt.Sprintf("-%04dw%02d", year, week)))
	})
})
