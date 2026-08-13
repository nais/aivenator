package kafka

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/constants"
	operator "github.com/nais/aivenator/pkg/aiven/operator"
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
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	aivenProjectName  = "a-project-name"
	ca                = "my-ca"
	teamNamespaceName = "test-ns"
	teamAppName       = "test-app"
	credStoreSecret   = "my-secret"
	invalidPool       = "not-my-testing-pool"
	secretName        = "my-individual-secret"
	serviceURI        = "example.com"
	serviceUserName   = "service-user-name"
	// deterministicUsername is the '_'-delimited username Kafka mints from the
	// AivenApplication; invalid as a k8s name, so it only rides in spec.username.
	deterministicUsername = "test-ns_test-app_2cf0e5d8_3D_"
)

type mockContainer struct {
	crServiceUser      *operator.MockServiceUserManager
	generator          *certificate.MockGenerator
	nameResolver       *liberator_service.MockNameResolver
	projectManager     *project.MockProjectManager
	serviceManager     *service.MockServiceManager
	serviceUserManager *serviceuser.MockServiceUserManager
}

func TestKafka(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Kafka Suite")
}

// fullRawSecret is the connection secret aiven-operator publishes for a
// reconciled Kafka ServiceUser CR, as Manager.CreateServiceUser returns it.
func fullRawSecret(username string) map[string]string {
	return map[string]string{
		operator.ServiceUserUsername:   username,
		operator.ServiceUserPassword:   "s3cret",
		operator.ServiceUserAccessCert: "access-cert",
		operator.ServiceUserAccessKey:  "access-key",
	}
}

var _ = Describe("kafka handler", func() {
	var mocks mockContainer
	var logger log.FieldLogger
	var applicationBuilder aiven_nais_io_v1.AivenApplicationBuilder
	var ctx context.Context
	var individualSecret *corev1.Secret
	var cancel context.CancelFunc
	var kafkaHandler KafkaHandler

	// newReader builds the client.Reader Kafka uses to read back the persisted
	// app secret (to recover the frozen CR name), seeded with the given secrets.
	newReader := func(objects ...client.Object) client.Reader {
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	}

	// persistedSecret is a prior app secret carrying the given serviceUser
	// annotation and stored username, as an earlier reconcile would have written it.
	persistedSecret := func(serviceUserAnnotation, username string) *corev1.Secret {
		s := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: teamNamespaceName,
				Annotations: map[string]string{
					ServiceUserAnnotation: serviceUserAnnotation,
					PoolAnnotation:        aivenProjectName,
					InstanceAnnotation:    aivenProjectName,
				},
			},
		}
		if username != "" {
			s.Data = map[string][]byte{KafkaSchemaUser: []byte(username)}
		}
		return s
	}

	// crNameFor recomputes the CR name the handler mints for an app, so adopt
	// specs can seed the exact annotation a prior reconcile wrote.
	crNameFor := func(app aiven_nais_io_v1.AivenApplication) string {
		return utils.ServiceUserName(app.GetName(), "", app.Spec.Kafka.Pool, app.Spec.Kafka.SecretName, time.Now())
	}

	// withReader swaps in a client.Reader; by default Kafka reads back an empty
	// cluster (fresh secret path).
	withReader := func(reader client.Reader) {
		kafkaHandler.k8sReader = reader
	}

	BeforeEach(func() {
		individualSecret = &corev1.Secret{}

		root := log.New()
		root.Out = GinkgoWriter
		logger = log.NewEntry(root)
		mocks = mockContainer{
			crServiceUser:      operator.NewMockServiceUserManager(GinkgoT()),
			generator:          certificate.NewMockGenerator(GinkgoT()),
			nameResolver:       liberator_service.NewMockNameResolver(GinkgoT()),
			projectManager:     project.NewMockProjectManager(GinkgoT()),
			serviceManager:     service.NewMockServiceManager(GinkgoT()),
			serviceUserManager: serviceuser.NewMockServiceUserManager(GinkgoT()),
		}
		kafkaHandler = KafkaHandler{
			crServiceUser: mocks.crServiceUser,
			generator:     mocks.generator,
			k8sReader:     newReader(),
			nameResolver:  mocks.nameResolver,
			project:       mocks.projectManager,
			projects:      []string{"dev-nais-dev", aivenProjectName},
			service:       mocks.serviceManager,
			serviceuser:   mocks.serviceUserManager,
			secretConfig: utils.SecretConfig{
				Project:     mocks.projectManager,
				ProjectName: aivenProjectName,
			},
		}
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)

		applicationBuilder = aiven_nais_io_v1.NewAivenApplicationBuilder(teamAppName, teamNamespaceName)
	})
	AfterEach(func() {
		cancel()
	})

	When("cleaning up", func() {
		It("no error when nothing to clean up", func() {
			err := kafkaHandler.Cleanup(ctx, individualSecret, logger)
			Expect(err).ToNot(HaveOccurred())
		})

		Context("the annotation is a valid CR name", func() {
			BeforeEach(func() {
				individualSecret.SetNamespace(teamNamespaceName)
				individualSecret.SetAnnotations(map[string]string{
					ServiceUserAnnotation: serviceUserName,
					PoolAnnotation:        aivenProjectName,
				})
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
			})

			It("deletes the ServiceUser CR", func() {
				mocks.crServiceUser.On("Exists", mock.Anything, teamNamespaceName, serviceUserName).Return(true, nil)
				mocks.crServiceUser.On("DeleteServiceUser", mock.Anything, teamNamespaceName, serviceUserName, mock.Anything).Return(nil)
				Expect(kafkaHandler.Cleanup(ctx, individualSecret, logger)).To(Succeed())
			})

			// A tracked legacy (pre-CR) user has no CR: the current CR is deleted, and
			// the legacy user is drained directly via the Aiven API.
			It("also drains a tracked legacy user via the direct API", func() {
				const legacyUser = "test-ns_test-app_old0_"
				individualSecret.SetAnnotations(map[string]string{
					ServiceUserAnnotation:       serviceUserName,
					PoolAnnotation:              aivenProjectName,
					LegacyServiceUserAnnotation: legacyUser,
				})
				mocks.crServiceUser.On("Exists", mock.Anything, teamNamespaceName, serviceUserName).Return(true, nil)
				mocks.crServiceUser.On("DeleteServiceUser", mock.Anything, teamNamespaceName, serviceUserName, mock.Anything).Return(nil)
				mocks.serviceUserManager.On("Delete", mock.Anything, legacyUser, aivenProjectName, "kafka", mock.Anything).Return(nil)
				Expect(kafkaHandler.Cleanup(ctx, individualSecret, logger)).To(Succeed())
			})

			// A valid CR name with the CR already gone: aiven-operator deleted the Aiven
			// user when the CR went away, so there is nothing to do.
			It("does nothing when the CR is already gone", func() {
				mocks.crServiceUser.On("Exists", mock.Anything, teamNamespaceName, serviceUserName).Return(false, nil)
				Expect(kafkaHandler.Cleanup(ctx, individualSecret, logger)).To(Succeed())
			})

			It("returns a server error from the delete", func() {
				mocks.crServiceUser.On("Exists", mock.Anything, teamNamespaceName, serviceUserName).Return(true, nil)
				mocks.crServiceUser.On("DeleteServiceUser", mock.Anything, teamNamespaceName, serviceUserName, mock.Anything).
					Return(aiven.Error{Message: "boom", Status: 500})
				Expect(kafkaHandler.Cleanup(ctx, individualSecret, logger)).ToNot(Succeed())
			})

			// A k8s NotFound from the CR delete (e.g. the CR vanished after Exists
			// reported it) is propagated, not tolerated.
			It("propagates a k8s NotFound from the CR delete", func() {
				mocks.crServiceUser.On("Exists", mock.Anything, teamNamespaceName, serviceUserName).Return(true, nil)
				mocks.crServiceUser.On("DeleteServiceUser", mock.Anything, teamNamespaceName, serviceUserName, mock.Anything).
					Return(k8serrors.NewNotFound(schema.GroupResource{Group: "aiven.io", Resource: "serviceusers"}, serviceUserName))
				Expect(kafkaHandler.Cleanup(ctx, individualSecret, logger)).ToNot(Succeed())
			})
		})

		Context("the annotation is a raw pre-migration username", func() {
			BeforeEach(func() {
				individualSecret.SetNamespace(teamNamespaceName)
				individualSecret.SetAnnotations(map[string]string{
					ServiceUserAnnotation: deterministicUsername,
					PoolAnnotation:        aivenProjectName,
				})
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
			})

			// Transitional: no CR exists for a raw '_'-delimited name (Exists=false),
			// so it is deleted directly via the Aiven API.
			It("deletes the user directly via the Aiven API", func() {
				mocks.serviceUserManager.On("Delete", mock.Anything, deterministicUsername, aivenProjectName, "kafka", mock.Anything).Return(nil)
				Expect(kafkaHandler.Cleanup(ctx, individualSecret, logger)).To(Succeed())
			})

			It("tolerates the user already being gone", func() {
				mocks.serviceUserManager.On("Delete", mock.Anything, deterministicUsername, aivenProjectName, "kafka", mock.Anything).
					Return(aiven.Error{Message: "Not Found", Status: 404})
				Expect(kafkaHandler.Cleanup(ctx, individualSecret, logger)).To(Succeed())
			})
		})

		Context("the kafka service name cannot be resolved", func() {
			BeforeEach(func() {
				individualSecret.SetAnnotations(map[string]string{
					ServiceUserAnnotation: serviceUserName,
					PoolAnnotation:        aivenProjectName,
				})
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("", errors.New("aiven unreachable"))
			})
			It("returns the error", func() {
				Expect(kafkaHandler.Cleanup(ctx, individualSecret, logger)).ToNot(Succeed())
			})
		})
	})

	When("there is an aiven application", func() {
		Context("that has no kafka configured", func() {
			It("should not return an error", func() {
				application := applicationBuilder.Build()
				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).ToNot(HaveOccurred())
				Expect(individualSecrets).To(BeNil())
			})
		})
		Context("that has kafka configured", func() {
			var kafkaServiceAddresses service.ServiceAddresses
			BeforeEach(func() {
				applicationBuilder = applicationBuilder.WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Kafka: &aiven_nais_io_v1.KafkaSpec{
						Pool:       aivenProjectName,
						SecretName: secretName,
					},
				})

				mock := service.MockServiceAddresses{}
				mock.EXPECT().Kafka().Return(service.ServiceAddress{URI: serviceURI})
				mock.EXPECT().SchemaRegistry().Return(service.ServiceAddress{})
				kafkaServiceAddresses = &mock
			})

			It("should return an error if the pool is invalid", func() {
				application := applicationBuilder.WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Kafka: &aiven_nais_io_v1.KafkaSpec{
						Pool: invalidPool,
					},
				}).Build()
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, invalidPool).Return("", utils.ErrUnrecoverable)

				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).To(HaveOccurred())
				Expect(individualSecrets).To(BeNil())
			})

			It("should return an error if the service user creation fails", func() {
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(kafkaServiceAddresses, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return(ca, nil)
				mocks.crServiceUser.On("FindAdoptable", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("", nil).Maybe()
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errors.New("failed to create"))

				application := applicationBuilder.Build()
				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).To(HaveOccurred())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
			})

			It("counts each retry on the pending secret and only escalates at the threshold", func() {
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(kafkaServiceAddresses, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return(ca, nil)
				mocks.crServiceUser.On("FindAdoptable", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("", nil).Maybe()
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, &utils.SecretNotReadyError{Namespace: teamNamespaceName, Secret: "raw-secret"})

				application := applicationBuilder.Build()
				var notReady *utils.SecretNotReadyError
				for attempt := 1; attempt <= utils.SecretMissEscalateThreshold; attempt++ {
					_, err := kafkaHandler.Apply(ctx, &application, logger)
					Expect(errors.As(err, &notReady)).To(BeTrue(), "attempt %d", attempt)
					cond := application.Status.GetConditionOfType(utils.PendingSecretConditionType("raw-secret"))
					Expect(cond).ToNot(BeNil(), "attempt %d", attempt)
					Expect(cond.Reason).To(Equal(strconv.Itoa(attempt)), "attempt %d", attempt)
					Expect(notReady.Escalated).To(Equal(attempt >= utils.SecretMissEscalateThreshold), "attempt %d", attempt)
				}
			})

			// Operator hasn't published the secret yet: the returned Secret lacks
			// keys, so Apply must fail rather than project blanks.
			It("fails when the published secret lacks a required key", func() {
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(kafkaServiceAddresses, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return(ca, nil)
				mocks.crServiceUser.On("FindAdoptable", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("", nil).Maybe()
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(&operator.ServiceUser{Username: deterministicUsername, Secret: map[string]string{}}, nil)

				application := applicationBuilder.Build()
				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).To(HaveOccurred())
				Expect(individualSecrets).To(BeNil())
			})

			// A non-NotFound error reading the persisted secret must fail the
			// reconcile (requeue), not be silently treated as "no annotation".
			It("propagates a non-NotFound error reading the existing secret", func() {
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(kafkaServiceAddresses, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return(ca, nil)
				withReader(erroringReader{err: errors.New("api server down")})

				application := applicationBuilder.Build()
				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).To(HaveOccurred())
				Expect(individualSecrets).To(BeNil())
			})

			// The CR name must come from utils.ServiceUserName (the shared scheme),
			// while the real '_'-delimited username rides in spec.username.
			// A recovered name's credentials were never delivered (the annotation and
			// the credentials persist together), so rollback cleans up the unused
			// account instead of stranding it.
			It("rolls back a recovered name nobody uses when credstore generation fails", func() {
				familyPrefix := utils.ServiceUserNamePrefix(teamAppName, "", aivenProjectName, secretName)
				staleName := familyPrefix + "-2026w01"
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(kafkaServiceAddresses, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return(ca, nil)
				mocks.crServiceUser.On("FindAdoptable", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(staleName, nil)
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(&operator.ServiceUser{Username: deterministicUsername, Secret: fullRawSecret(deterministicUsername)}, nil)
				mocks.generator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("boom"))
				mocks.crServiceUser.On("Exists", mock.Anything, teamNamespaceName, staleName).Return(true, nil)
				mocks.crServiceUser.On("DeleteServiceUser", mock.Anything, teamNamespaceName, staleName, mock.Anything).Return(nil)

				application := applicationBuilder.Build()
				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).To(HaveOccurred())
				Expect(individualSecrets).To(BeNil())
				mocks.crServiceUser.AssertCalled(GinkgoT(), "DeleteServiceUser", mock.Anything, teamNamespaceName, staleName, mock.Anything)
				// Pins the family tuple: a swap of the fold's string args would derive
				// a different prefix and silently kill recovery in production.
				mocks.crServiceUser.AssertCalled(GinkgoT(), "FindAdoptable", mock.Anything, teamNamespaceName, teamAppName, familyPrefix, "kafka", mock.Anything)
			})

			// Kafka usernames are deterministic per generation, so a fresh CR can
			// reproduce the live legacy username and the operator adopts that account;
			// rolling it back would delete credentials pods still use.
			It("does not roll back a fresh account that took over the live legacy user", func() {
				withReader(newReader(persistedSecret(deterministicUsername, "")))
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(kafkaServiceAddresses, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return(ca, nil)
				mocks.crServiceUser.On("FindAdoptable", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("", nil)
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(&operator.ServiceUser{Username: deterministicUsername, Secret: fullRawSecret(deterministicUsername)}, nil)
				mocks.generator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("boom"))

				application := applicationBuilder.Build()
				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).To(HaveOccurred())
				Expect(individualSecrets).To(BeNil())
				mocks.crServiceUser.AssertNotCalled(GinkgoT(), "DeleteServiceUser", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
				mocks.crServiceUser.AssertNotCalled(GinkgoT(), "Exists", mock.Anything, mock.Anything, mock.Anything)
			})

			// A recovered CR exists and declared its identity at creation; per the
			// created-only rule nothing is declared here.
			It("re-uses a recovered name and leaves the username to the CR", func() {
				staleName := utils.ServiceUserNamePrefix(teamAppName, "", aivenProjectName, secretName) + "-2026w01"
				var captured operator.ServiceUserSpec
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(kafkaServiceAddresses, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return(ca, nil)
				mocks.generator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
					Return(&certificate.CredStoreData{Keystore: []byte("k"), Truststore: []byte("t"), Secret: credStoreSecret}, nil)
				mocks.crServiceUser.On("FindAdoptable", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(staleName, nil)
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.MatchedBy(func(spec operator.ServiceUserSpec) bool {
					captured = spec
					return true
				}), mock.Anything).Return(&operator.ServiceUser{Username: deterministicUsername, Secret: fullRawSecret(deterministicUsername)}, nil)

				application := applicationBuilder.Build()
				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).ToNot(HaveOccurred())
				Expect(captured.Name).To(Equal(staleName))
				Expect(captured.Username).To(BeEmpty())
				Expect(individualSecrets[0].GetAnnotations()[ServiceUserAnnotation]).To(Equal(staleName))
			})

			It("mints a valid CR name and carries the real username in spec.username", func() {
				var captured operator.ServiceUserSpec
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(kafkaServiceAddresses, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return(ca, nil)
				mocks.generator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
					Return(&certificate.CredStoreData{Keystore: []byte("k"), Truststore: []byte("t"), Secret: credStoreSecret}, nil)
				mocks.crServiceUser.On("FindAdoptable", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("", nil).Maybe()
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.MatchedBy(func(spec operator.ServiceUserSpec) bool {
					captured = spec
					return true
				}), mock.Anything).Return(&operator.ServiceUser{Username: deterministicUsername, Secret: fullRawSecret(deterministicUsername)}, nil)

				application := applicationBuilder.Build()
				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).ToNot(HaveOccurred())
				Expect(captured.Username).To(Equal(deterministicUsername))
				Expect(utils.IsValidCRName(captured.Name)).To(BeTrue())
				Expect(captured.Name).To(HavePrefix(teamAppName + "-"))
				Expect(captured.Name).ToNot(Equal(deterministicUsername))
				// The frozen CR name, not the raw username, is annotated for reuse.
				Expect(individualSecrets[0].GetAnnotations()[ServiceUserAnnotation]).To(Equal(captured.Name))
			})

			It("produces a complete secret", func() {
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(kafkaServiceAddresses, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return(ca, nil)
				mocks.generator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
					Return(&certificate.CredStoreData{Keystore: []byte("my-keystore"), Truststore: []byte("my-truststore"), Secret: credStoreSecret}, nil)
				mocks.crServiceUser.On("FindAdoptable", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("", nil).Maybe()
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(&operator.ServiceUser{Username: deterministicUsername, Secret: fullRawSecret(deterministicUsername)}, nil)

				application := applicationBuilder.Build()
				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).ToNot(HaveOccurred())
				Expect(individualSecrets[0].Name).To(Equal(secretName))
				Expect(individualSecrets[0].Finalizers).To(ConsistOf(constants.AivenatorFinalizer))
				Expect(individualSecrets[0].GetAnnotations()).To(HaveKeyWithValue(PoolAnnotation, aivenProjectName))
				Expect(individualSecrets[0].GetAnnotations()).To(HaveKeyWithValue(InstanceAnnotation, aivenProjectName))
				Expect(utils.KeysFromStringMap(individualSecrets[0].StringData)).To(ContainElements(
					KafkaCA, KafkaPrivateKey, KafkaCredStorePassword, KafkaSchemaRegistry, KafkaSchemaUser, KafkaSchemaPassword,
					KafkaBrokers, KafkaSecretUpdated, KafkaCertificate, utils.AivenCAKey,
				))
				Expect(keysFromByteMap(individualSecrets[0].Data)).To(ConsistOf(KafkaKeystore, KafkaTruststore))
				Expect(individualSecrets[0].StringData[KafkaSchemaUser]).To(Equal(deterministicUsername))
				Expect(validation.ValidateAnnotations(individualSecrets[0].GetAnnotations(), field.NewPath("metadata.annotations"))).To(BeEmpty())

				// Strict: the secret carries exactly this annotation set (the CR name is
				// dynamic, so read it back) and no more.
				crName := individualSecrets[0].GetAnnotations()[ServiceUserAnnotation]
				Expect(individualSecrets[0]).To(Equal(corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      secretName,
						Namespace: application.GetNamespace(),
						Annotations: map[string]string{
							ServiceUserAnnotation:             crName,
							InstanceAnnotation:                aivenProjectName,
							PoolAnnotation:                    aivenProjectName,
							constants.AivenatorProtectedKey:   "false",
							"nais.io/deploymentCorrelationID": "",
						},
						Labels:     individualSecrets[0].Labels,
						Finalizers: []string{constants.AivenatorFinalizer},
					},
					Data:       individualSecrets[0].Data,
					StringData: individualSecrets[0].StringData,
				}))
			})

			// A new secretName is a rotation: it mints a fresh CR name (keyed on
			// secretName) and a fresh liberator username, so the operator provisions a
			// new Aiven user rather than reusing the previous secret's.
			It("rotates the CR name and the Aiven username when the trigger changes", func() {
				var captured []operator.ServiceUserSpec
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(kafkaServiceAddresses, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return(ca, nil)
				mocks.generator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
					Return(&certificate.CredStoreData{Keystore: []byte("k"), Truststore: []byte("t"), Secret: credStoreSecret}, nil)
				mocks.crServiceUser.On("FindAdoptable", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("", nil).Maybe()
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.MatchedBy(func(spec operator.ServiceUserSpec) bool {
					captured = append(captured, spec)
					return true
				}), mock.Anything).Return(&operator.ServiceUser{Username: deterministicUsername, Secret: fullRawSecret(deterministicUsername)}, nil)

				for i, sn := range []string{"secret-week-30", "secret-week-31"} {
					app := aiven_nais_io_v1.NewAivenApplicationBuilder(teamAppName, teamNamespaceName).WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						Kafka: &aiven_nais_io_v1.KafkaSpec{Pool: aivenProjectName, SecretName: sn},
					}).Build()
					app.Generation = int64(i + 1) // the trigger change bumps the aivenapp generation
					_, err := kafkaHandler.Apply(ctx, &app, logger)
					Expect(err).ToNot(HaveOccurred())
				}
				Expect(captured).To(HaveLen(2))
				Expect(captured[0].Name).ToNot(Equal(captured[1].Name))
				Expect(captured[0].Username).ToNot(Equal(captured[1].Username))
			})

			// Pre-CR (direct-API) secret: the annotation holds a raw '_'-delimited
			// username that can't be a CR name. Mint a fresh CR user for the current
			// secret and record the old username for the direct-API drain in Cleanup.
			It("mints a fresh user and records the legacy username for a pre-CR secret", func() {
				const rawUser = "test-ns_test-app_old0_" // raw, invalid as a CR name
				Expect(utils.IsValidCRName(rawUser)).To(BeFalse())
				var captured operator.ServiceUserSpec
				withReader(newReader(persistedSecret(rawUser, rawUser)))
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(kafkaServiceAddresses, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return(ca, nil)
				mocks.generator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
					Return(&certificate.CredStoreData{Keystore: []byte("k"), Truststore: []byte("t"), Secret: credStoreSecret}, nil)
				mocks.crServiceUser.On("FindAdoptable", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("", nil).Maybe()
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.MatchedBy(func(spec operator.ServiceUserSpec) bool {
					captured = spec
					return true
				}), mock.Anything).Return(&operator.ServiceUser{Username: deterministicUsername, Secret: fullRawSecret(deterministicUsername)}, nil)

				application := applicationBuilder.Build()
				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).ToNot(HaveOccurred())
				Expect(utils.IsValidCRName(captured.Name)).To(BeTrue())
				Expect(captured.Username).To(Equal(deterministicUsername)) // fresh mint, not the raw user
				Expect(captured.Username).ToNot(Equal(rawUser))
				Expect(individualSecrets[0].GetAnnotations()).To(HaveKeyWithValue(LegacyServiceUserAnnotation, rawUser))
			})

			// Unchanged secret: the annotated CR still targets this service, so its frozen
			// CR name is reused and aivenator leaves spec.username unset — the operator
			// keeps the CR's immutable value, which still lands in the secret.
			It("adopts the frozen CR name and leaves the username to the CR", func() {
				const keptUser = "test-ns_test-app_keep0_"
				var captured operator.ServiceUserSpec
				application := applicationBuilder.Build()
				crName := crNameFor(application)
				withReader(newReader(persistedSecret(crName, keptUser)))
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.crServiceUser.On("ServiceName", mock.Anything, teamNamespaceName, crName).Return("kafka", true, nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(kafkaServiceAddresses, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return(ca, nil)
				mocks.generator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
					Return(&certificate.CredStoreData{Keystore: []byte("k"), Truststore: []byte("t"), Secret: credStoreSecret}, nil)
				mocks.crServiceUser.On("FindAdoptable", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("", nil).Maybe()
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.MatchedBy(func(spec operator.ServiceUserSpec) bool {
					captured = spec
					return true
				}), mock.Anything).Return(&operator.ServiceUser{Username: keptUser, Secret: fullRawSecret(keptUser)}, nil)

				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).ToNot(HaveOccurred())
				Expect(captured.Name).To(Equal(crName)) // frozen, not re-minted
				Expect(captured.Username).To(BeEmpty()) // left unset so the operator keeps the CR's value
				Expect(individualSecrets[0].StringData[KafkaSchemaUser]).To(Equal(keptUser))
				Expect(individualSecrets[0].GetAnnotations()).ToNot(HaveKey(LegacyServiceUserAnnotation))
			})

			// A generation bump with the secret name unchanged (e.g. editing the OpenSearch
			// block of a Kafka+OpenSearch app) must not touch spec.username: aivenator leaves
			// it unset on adopt, so the generation-derived name is never written and the
			// immutable field is never rewritten. This is the rotation-deadlock the design removes.
			It("never sets spec.username on a generation bump with an unchanged secret", func() {
				const keptUser = "test-ns_test-app_keep0_"
				var captured operator.ServiceUserSpec
				application := applicationBuilder.Build()
				application.Generation = 7 // bumped since the user was minted
				crName := crNameFor(application)
				withReader(newReader(persistedSecret(crName, keptUser)))
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.crServiceUser.On("ServiceName", mock.Anything, teamNamespaceName, crName).Return("kafka", true, nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(kafkaServiceAddresses, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return(ca, nil)
				mocks.generator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
					Return(&certificate.CredStoreData{Keystore: []byte("k"), Truststore: []byte("t"), Secret: credStoreSecret}, nil)
				mocks.crServiceUser.On("FindAdoptable", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("", nil).Maybe()
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.MatchedBy(func(spec operator.ServiceUserSpec) bool {
					captured = spec
					return true
				}), mock.Anything).Return(&operator.ServiceUser{Username: keptUser, Secret: fullRawSecret(keptUser)}, nil)

				_, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).ToNot(HaveOccurred())
				Expect(captured.Username).To(BeEmpty()) // never recomputed from the bumped generation
			})

			// Switching the pool without rotating the secret name is illegal: the frozen
			// CR is bound to the old pool's service (immutable), so provisioning must fail
			// rather than mint a colliding user or re-point the CR.
			It("fails when the frozen CR targets a different Kafka service", func() {
				application := applicationBuilder.Build()
				crName := crNameFor(application)
				withReader(newReader(persistedSecret(crName, "test-ns_test-app_keep0_")))
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.crServiceUser.On("ServiceName", mock.Anything, teamNamespaceName, crName).Return("kafka-other-pool", true, nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(kafkaServiceAddresses, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return(ca, nil)

				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(errors.Is(err, utils.ErrUnrecoverable)).To(BeTrue())
				Expect(individualSecrets).To(BeNil())
			})

			// Re-creating a vanished CR is a creation, so the account identity must be
			// declared: left empty, the operator would default it to the CR's own name,
			// which no kafka ACL pattern matches.
			It("declares the username when re-creating a vanished CR", func() {
				var captured operator.ServiceUserSpec
				application := applicationBuilder.Build()
				crName := crNameFor(application)
				withReader(newReader(persistedSecret(crName, "")))
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.crServiceUser.On("ServiceName", mock.Anything, teamNamespaceName, crName).Return("", false, nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(kafkaServiceAddresses, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return(ca, nil)
				mocks.generator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
					Return(&certificate.CredStoreData{Keystore: []byte("k"), Truststore: []byte("t"), Secret: credStoreSecret}, nil)
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.MatchedBy(func(spec operator.ServiceUserSpec) bool {
					captured = spec
					return true
				}), mock.Anything).Return(&operator.ServiceUser{Username: deterministicUsername, Secret: fullRawSecret(deterministicUsername)}, nil)

				_, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).ToNot(HaveOccurred())
				Expect(captured.Name).To(Equal(crName)) // frozen name kept for cleanup continuity
				Expect(captured.Username).To(Equal(deterministicUsername))
			})

			// The stored username is no longer an input: an adopted secret with the CR-name
			// annotation but no stored username still provisions, because aivenator leaves
			// spec.username to the immutable CR rather than reading it back.
			It("adopts without a stored username", func() {
				const keptUser = "test-ns_test-app_keep0_"
				var captured operator.ServiceUserSpec
				application := applicationBuilder.Build()
				crName := crNameFor(application)
				withReader(newReader(persistedSecret(crName, ""))) // annotation present, KafkaSchemaUser absent
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.crServiceUser.On("ServiceName", mock.Anything, teamNamespaceName, crName).Return("kafka", true, nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(kafkaServiceAddresses, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return(ca, nil)
				mocks.generator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
					Return(&certificate.CredStoreData{Keystore: []byte("k"), Truststore: []byte("t"), Secret: credStoreSecret}, nil)
				mocks.crServiceUser.On("FindAdoptable", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("", nil).Maybe()
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.MatchedBy(func(spec operator.ServiceUserSpec) bool {
					captured = spec
					return true
				}), mock.Anything).Return(&operator.ServiceUser{Username: keptUser, Secret: fullRawSecret(keptUser)}, nil)

				_, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).ToNot(HaveOccurred())
				Expect(captured.Name).To(Equal(crName))
				Expect(captured.Username).To(BeEmpty())
			})

			It("should fail when there is no service", func() {
				application := applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						Kafka: &aiven_nais_io_v1.KafkaSpec{Pool: aivenProjectName},
					}).
					Build()
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, aiven.Error{Message: "aiven-error", MoreInfo: "aiven-more-info", Status: 500})

				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).To(HaveOccurred())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
			})

			It("fails when there is no CA", func() {
				application := applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						Kafka: &aiven_nais_io_v1.KafkaSpec{Pool: aivenProjectName},
					}).
					Build()
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(kafkaServiceAddresses, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).
					Return("", aiven.Error{Message: "aiven-error", MoreInfo: "aiven-more-info", Status: 500})

				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)
				Expect(err).To(HaveOccurred())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
			})

			It("Errors on specifically the pool called not-my-testing-pool", func() {
				application := applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						Kafka: &aiven_nais_io_v1.KafkaSpec{Pool: invalidPool},
					}).
					Build()
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, invalidPool).Return("", utils.ErrUnrecoverable)

				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, utils.ErrUnrecoverable)).To(BeTrue())
				Expect(individualSecrets).To(BeNil())
			})

			// MakeCredStores fails after the CR exists: Apply rolls back by deleting
			// the freshly-created CR through Cleanup.
			It("fails and cleans up when makecredstores fails", func() {
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(kafkaServiceAddresses, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return(ca, nil)
				mocks.crServiceUser.On("FindAdoptable", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("", nil).Maybe()
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(&operator.ServiceUser{Username: deterministicUsername, Secret: fullRawSecret(deterministicUsername)}, nil)
				mocks.generator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("local-fail"))
				mocks.crServiceUser.On("Exists", mock.Anything, teamNamespaceName, mock.Anything).Return(true, nil)
				mocks.crServiceUser.On("DeleteServiceUser", mock.Anything, teamNamespaceName, mock.Anything, mock.Anything).Return(nil)

				application := applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						Kafka: &aiven_nais_io_v1.KafkaSpec{Pool: aivenProjectName, SecretName: secretName},
					}).
					Build()

				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).To(HaveOccurred())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationLocalFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
			})

			// MakeCredStores fails on an adopted user: the rollback must not delete the
			// live service user, so no Exists/DeleteServiceUser calls are set up.
			It("keeps the adopted service user when makecredstores fails", func() {
				const keptUser = "test-ns_test-app_keep0_"
				application := applicationBuilder.Build()
				crName := crNameFor(application)
				withReader(newReader(persistedSecret(crName, keptUser)))
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.crServiceUser.On("ServiceName", mock.Anything, teamNamespaceName, crName).Return("kafka", true, nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(kafkaServiceAddresses, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return(ca, nil)
				mocks.crServiceUser.On("FindAdoptable", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("", nil).Maybe()
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(&operator.ServiceUser{Username: keptUser, Secret: fullRawSecret(keptUser)}, nil)
				mocks.generator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("local-fail"))

				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).To(HaveOccurred())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationLocalFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
			})

			// A pre-CR migration mint is exactly as new as a from-scratch mint: the CR
			// didn't exist a moment ago, so a MakeCredStores failure must roll it back
			// too, the same as the fresh-mint case above. createdFresh must not be keyed
			// off the old secret's raw legacy annotation, which is always present here.
			It("cleans up the freshly-minted CR when makecredstores fails during a pre-CR migration", func() {
				const rawUser = "test-ns_test-app_old0_" // raw, invalid as a CR name
				withReader(newReader(persistedSecret(rawUser, rawUser)))
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddressesFromCache", mock.Anything, mock.Anything, mock.Anything).
					Return(kafkaServiceAddresses, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return(ca, nil)
				mocks.crServiceUser.On("FindAdoptable", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("", nil).Maybe()
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(&operator.ServiceUser{Username: deterministicUsername, Secret: fullRawSecret(deterministicUsername)}, nil)
				mocks.generator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("local-fail"))
				mocks.crServiceUser.On("Exists", mock.Anything, teamNamespaceName, mock.Anything).Return(true, nil)
				mocks.crServiceUser.On("DeleteServiceUser", mock.Anything, teamNamespaceName, mock.Anything, mock.Anything).Return(nil)

				application := applicationBuilder.Build()
				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).To(HaveOccurred())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationLocalFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
			})
		})
	})
})

// erroringReader is a client.Reader whose Get always fails, to exercise the
// non-NotFound read path in provideServiceUser.
type erroringReader struct{ err error }

func (r erroringReader) Get(_ context.Context, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
	return r.err
}

func (r erroringReader) List(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
	return r.err
}

func keysFromByteMap(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
