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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

const (
	aivenProjectName = "a-project-name"
	ca               = "my-ca"
	credStoreSecret  = "my-secret"
	invalidPool      = "not-my-testing-pool"
	secretName       = "my-individual-secret"
	serviceURI       = "http://example.com"
	serviceUserName  = "service-user-name"
)

type mockContainer struct {
	projectManager     *project.MockProjectManager
	serviceUserManager *serviceuser.MockServiceUserManager
	serviceManager     *service.MockServiceManager
	nameResolver       *liberator_service.MockNameResolver
	generator          *certificate.MockGenerator
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
	var individualSecret *corev1.Secret
	var cancel context.CancelFunc
	var kafkaHandler KafkaHandler

	BeforeEach(func() {
		individualSecret = &corev1.Secret{}

		root := log.New()
		root.Out = GinkgoWriter
		logger = log.NewEntry(root)
		mocks = mockContainer{
			projectManager:     project.NewMockProjectManager(GinkgoT()),
			serviceUserManager: serviceuser.NewMockServiceUserManager(GinkgoT()),
			serviceManager:     service.NewMockServiceManager(GinkgoT()),
			nameResolver:       liberator_service.NewMockNameResolver(GinkgoT()),
			generator:          certificate.NewMockGenerator(GinkgoT()),
		}
		kafkaHandler = KafkaHandler{
			project:      mocks.projectManager,
			serviceuser:  mocks.serviceUserManager,
			service:      mocks.serviceManager,
			generator:    mocks.generator,
			nameResolver: mocks.nameResolver,
			secretConfig: utils.SecretConfig{
				Project:     mocks.projectManager,
				ProjectName: aivenProjectName,
			},
			projects: []string{"dev-nais-dev", aivenProjectName},
		}
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)

		applicationBuilder = aiven_nais_io_v1.NewAivenApplicationBuilder("test-app", "test-ns")
	})
	AfterEach(func() {
		cancel()
	})

	When("no kafka is configured", func() {
		It("no error on cleanup", func() {
			err := kafkaHandler.Cleanup(ctx, individualSecret, logger)

			Expect(err).ToNot(HaveOccurred())
		})
		Context("delete serviceUser on cleanup", func() {
			BeforeEach(func() {
				individualSecret.SetAnnotations(map[string]string{
					ServiceUserAnnotation: serviceUserName,
					PoolAnnotation:        aivenProjectName,
				})
				mocks.serviceUserManager.On("Delete", mock.Anything, serviceUserName, aivenProjectName, mock.Anything, mock.Anything).Return(nil)
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
			})
			It("should not error", func() {
				err := kafkaHandler.Cleanup(ctx, individualSecret, logger)
				Expect(err).ToNot(HaveOccurred())
			})
		})
		Context("delete serviceUser on cleanup, but already gone", func() {
			BeforeEach(func() {
				individualSecret.SetAnnotations(map[string]string{
					ServiceUserAnnotation: serviceUserName,
					PoolAnnotation:        aivenProjectName,
				})
				mocks.serviceUserManager.On("Delete", mock.Anything, serviceUserName, aivenProjectName, mock.Anything, mock.Anything).Return(aiven.Error{
					Message: "Not Found",
					Status:  404,
				})
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
			})
			It("should not return an error", func() {
				err := kafkaHandler.Cleanup(ctx, individualSecret, logger)
				Expect(err).ToNot(HaveOccurred())
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
			BeforeEach(func() {
				applicationBuilder = applicationBuilder.WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					Kafka: &aiven_nais_io_v1.KafkaSpec{
						Pool:       aivenProjectName,
						SecretName: secretName,
					},
				})
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

				mocks.serviceManager.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
					Return(&service.ServiceAddresses{
						ServiceURI: serviceURI,
						SchemaRegistry: service.ServiceAddress{
							URI:  "",
							Host: "",
							Port: 0,
						},
					}, nil)

				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).
					Return(ca, nil)

				mocks.serviceUserManager.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, aiven.Error{
						Message:  "aiven-error",
						MoreInfo: "aiven-more-info",
						Status:   404,
					})

				mocks.serviceUserManager.On("Create", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, aiven.Error{Message: "aiven-error", Status: 500})

				application := applicationBuilder.Build()
				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).To(HaveOccurred())
				Expect(individualSecrets).To(BeNil())
			})

			It("should re-use supplied secret", func() {
				mocks.serviceUserManager.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(&aiven.ServiceUser{Username: serviceUserName}, nil)
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
					Return(&service.ServiceAddresses{
						ServiceURI: serviceURI,
						SchemaRegistry: service.ServiceAddress{
							URI:  "",
							Host: "",
							Port: 0,
						},
					}, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).
					Return(ca, nil)
				mocks.generator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
					Return(&certificate.CredStoreData{
						Keystore:   []byte("my-keystore"),
						Truststore: []byte("my-truststore"),
						Secret:     credStoreSecret,
					}, nil)

				application := applicationBuilder.Build()
				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).ToNot(HaveOccurred())

				expected := corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      application.Spec.Kafka.SecretName,
						Namespace: application.GetNamespace(),
						Annotations: map[string]string{
							ServiceUserAnnotation:             serviceUserName,
							constants.AivenatorProtectedKey:   "false",
							"nais.io/deploymentCorrelationID": "",
							PoolAnnotation:                    aivenProjectName,
						},
						Labels:     individualSecrets[0].Labels,
						Finalizers: []string{constants.AivenatorFinalizer},
					},
					Data:       individualSecrets[0].Data,
					StringData: individualSecrets[0].StringData,
				}

				Expect(expected).To(Equal(individualSecrets[0]))
				Expect(utils.KeysFromStringMap(individualSecrets[0].StringData)).To(ContainElements(
					KafkaCA, KafkaPrivateKey, KafkaCredStorePassword, KafkaSchemaRegistry, KafkaSchemaUser, KafkaSchemaPassword,
					KafkaBrokers, KafkaSecretUpdated, KafkaCertificate, utils.AivenCAKey,
				))
				Expect(keysFromByteMap(individualSecrets[0].Data)).To(ConsistOf(
					KafkaKeystore, KafkaTruststore,
				))
				Expect(validation.ValidateAnnotations(individualSecrets[0].GetAnnotations(), field.NewPath("metadata.annotations"))).To(BeEmpty())
				Expect(individualSecrets[0].GetAnnotations()[ServiceUserAnnotation]).To(Equal(serviceUserName))
			})

			It("should fail when there is no service", func() {
				application := applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						Kafka: &aiven_nais_io_v1.KafkaSpec{
							Pool: aivenProjectName,
						},
					}).
					Build()
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)

				mocks.serviceManager.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, aiven.Error{
						Message:  "aiven-error",
						MoreInfo: "aiven-more-info",
						Status:   500,
					})
				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).To(HaveOccurred())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
			})

			It("fails when there is no CA", func() {
				application := applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						Kafka: &aiven_nais_io_v1.KafkaSpec{
							Pool: aivenProjectName,
						},
					}).
					Build()
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
					Return(&service.ServiceAddresses{
						ServiceURI: serviceURI,
						SchemaRegistry: service.ServiceAddress{
							URI:  "",
							Host: "",
							Port: 0,
						},
					}, nil)

				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).
					Return("", aiven.Error{
						Message:  "aiven-error",
						MoreInfo: "aiven-more-info",
						Status:   500,
					})

				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)
				Expect(err).To(HaveOccurred())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
			})

			It("fails when failing to create serviceusers", func() {
				application := applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						Kafka: &aiven_nais_io_v1.KafkaSpec{
							Pool:       aivenProjectName,
							SecretName: secretName,
						},
					}).
					Build()
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
					Return(&service.ServiceAddresses{
						ServiceURI: serviceURI,
						SchemaRegistry: service.ServiceAddress{
							URI:  "",
							Host: "",
							Port: 0,
						},
					}, nil)
				mocks.serviceUserManager.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, aiven.Error{
						Message:  "aiven-error",
						MoreInfo: "aiven-more-info",
						Status:   404,
					})

				mocks.serviceUserManager.On("Create", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, aiven.Error{
						Message:  "aiven-error",
						MoreInfo: "aiven-more-info",
						Status:   500,
					})
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).
					Return(ca, nil)

				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)
				Expect(err).To(HaveOccurred())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
			})
			It("succeeds when succesfully creating missing serviceusers", func() {
				application := applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						Kafka: &aiven_nais_io_v1.KafkaSpec{
							Pool:       aivenProjectName,
							SecretName: secretName,
						},
					}).
					Build()
				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
					Return(&service.ServiceAddresses{
						ServiceURI: serviceURI,
						SchemaRegistry: service.ServiceAddress{
							URI:  "",
							Host: "",
							Port: 0,
						},
					}, nil)
				mocks.serviceUserManager.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, aiven.Error{
						Message:  "aiven-error",
						MoreInfo: "aiven-more-info",
						Status:   404,
					})
				mocks.serviceUserManager.On("Create", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(&aiven.ServiceUser{
						Username: serviceUserName,
					}, nil)
				mocks.generator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
					Return(&certificate.CredStoreData{
						Keystore:   []byte("my-keystore"),
						Truststore: []byte("my-truststore"),
						Secret:     credStoreSecret,
					}, nil)

				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).
					Return(ca, nil)
				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).ToNot(HaveOccurred())

				expected := corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      application.Spec.Kafka.SecretName,
						Namespace: application.GetNamespace(),
						Annotations: map[string]string{
							ServiceUserAnnotation:             serviceUserName,
							constants.AivenatorProtectedKey:   "false",
							"nais.io/deploymentCorrelationID": "",
							PoolAnnotation:                    aivenProjectName,
						},
						Labels:     individualSecrets[0].Labels,
						Finalizers: []string{constants.AivenatorFinalizer},
					},
					Data:       individualSecrets[0].Data,
					StringData: individualSecrets[0].StringData,
				}

				Expect(individualSecrets[0]).To(Equal(expected))
			})
			It("doesnt create a new serviceUser if already extant", func() {
				application := applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						Kafka: &aiven_nais_io_v1.KafkaSpec{
							Pool:       aivenProjectName,
							SecretName: secretName,
						},
					}).
					Build()

				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
					Return(&service.ServiceAddresses{
						ServiceURI: serviceURI,
						SchemaRegistry: service.ServiceAddress{
							URI:  "",
							Host: "",
							Port: 0,
						},
					}, nil)
				mocks.generator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
					Return(&certificate.CredStoreData{
						Keystore:   []byte("my-keystore"),
						Truststore: []byte("my-truststore"),
						Secret:     credStoreSecret,
					}, nil)

				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).
					Return(ca, nil)

				mocks.serviceUserManager.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(&aiven.ServiceUser{
						Username: serviceUserName,
					}, nil)

				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(mocks.serviceUserManager.AssertNotCalled(GinkgoT(), "Create",
					mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)).To(BeTrue())

				Expect(err).NotTo(HaveOccurred())

				expected := corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      application.Spec.Kafka.SecretName,
						Namespace: application.GetNamespace(),
						Annotations: map[string]string{
							"nais.io/deploymentCorrelationID": "",
							constants.AivenatorProtectedKey:   "false",
							ServiceUserAnnotation:             serviceUserName,
							PoolAnnotation:                    aivenProjectName,
						},
						Labels:     individualSecrets[0].Labels,
						Finalizers: []string{constants.AivenatorFinalizer},
					},
					Data:       individualSecrets[0].Data,
					StringData: individualSecrets[0].StringData,
				}

				Expect(individualSecrets[0]).To(Equal(expected))
			})

			It("Errors on specifically the pool called not-my-testing-pool", func() {
				application := applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						Kafka: &aiven_nais_io_v1.KafkaSpec{
							Pool: invalidPool,
						},
					}).
					Build()

				mocks.nameResolver.
					On("ResolveKafkaServiceName", mock.Anything, invalidPool).
					Return("", utils.ErrUnrecoverable)

				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, utils.ErrUnrecoverable)).To(BeTrue())
				Expect(individualSecrets).To(BeNil())
			})
			It("fails when makecredstores fails", func() {
				mocks.serviceUserManager.On("Delete", mock.Anything, serviceUserName, aivenProjectName, mock.Anything, mock.Anything).Return(nil)

				application := applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						Kafka: &aiven_nais_io_v1.KafkaSpec{
							Pool:       aivenProjectName,
							SecretName: secretName,
						},
					}).
					Build()

				mocks.nameResolver.On("ResolveKafkaServiceName", mock.Anything, aivenProjectName).Return("kafka", nil)
				mocks.serviceManager.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
					Return(&service.ServiceAddresses{
						ServiceURI: serviceURI,
						SchemaRegistry: service.ServiceAddress{
							URI:  "",
							Host: "",
							Port: 0,
						},
					}, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return(ca, nil)
				mocks.serviceUserManager.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, aiven.Error{Status: 404})
				mocks.serviceUserManager.On("Create", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(&aiven.ServiceUser{Username: serviceUserName}, nil)
				mocks.generator.On("MakeCredStores", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("local-fail"))

				individualSecrets, err := kafkaHandler.Apply(ctx, &application, logger)

				Expect(err).To(HaveOccurred())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationLocalFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
			})
		})
	})
})

func keysFromByteMap(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
