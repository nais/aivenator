package opensearch

import (
	"context"
	"testing"
	"time"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/aiven/opensearch"
	"github.com/nais/aivenator/pkg/aiven/project"
	"github.com/nais/aivenator/pkg/aiven/service"
	"github.com/nais/aivenator/pkg/aiven/serviceuser"
	"github.com/nais/aivenator/pkg/handlers/secret"
	"github.com/nais/aivenator/pkg/utils"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	appName         = "aiven-app"
	namespace       = "team-a"
	serviceUserName = "team-a"
	servicePassword = "service-password"
	projectName     = "my-project"
	serviceURI      = "http://example.com:1234"
	serviceHost     = "example.com"
	servicePort     = 1234
	instance        = "my-instance"
	serviceName     = "my-service"
	access          = "read"
)

type testData struct {
	instanceName             string
	serviceName              string
	serviceURI               string
	serviceHost              string
	servicePort              int
	access                   string
	username                 string
	serviceNameAnnotationKey string
	serviceUserAnnotationKey string
	usernameKey              string
	passwordKey              string
	uriKey                   string
	hostKey                  string
	portKey                  string
}

type mockContainer struct {
	serviceUserManager *serviceuser.MockServiceUserManager
	serviceManager     *service.MockServiceManager
	projectManager     *project.MockProjectManager
	aclManager         *opensearch.MockACLManager
}

func TestOpensearch(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Opensearch Suite")
}

var _ = Describe("opensearch handler", func() {
	var mocks mockContainer
	var data testData
	var logger log.FieldLogger
	var applicationBuilder aiven_nais_io_v1.AivenApplicationBuilder
	var ctx context.Context
	var sharedSecret corev1.Secret
	//	var individualSecret corev1.Secret
	var cancel context.CancelFunc
	var opensearchHandler OpenSearchHandler
	var application aiven_nais_io_v1.AivenApplication

	defaultServiceManagerMock := func(data testData) {
		mocks.serviceManager.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
			Return(&service.ServiceAddresses{
				OpenSearch: service.ServiceAddress{
					URI:  data.serviceURI,
					Host: data.serviceHost,
					Port: data.servicePort,
				},
			}, nil)
	}

	BeforeEach(func() {
		sharedSecret = corev1.Secret{}

		root := log.New()
		root.Out = GinkgoWriter
		logger = log.NewEntry(root)
		//		applicationBuilder = aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace)
		mocks = mockContainer{
			serviceUserManager: serviceuser.NewMockServiceUserManager(GinkgoT()),
			serviceManager:     service.NewMockServiceManager(GinkgoT()),
			projectManager:     project.NewMockProjectManager(GinkgoT()),
			aclManager:         opensearch.NewMockACLManager(GinkgoT()),
		}
		opensearchHandler = OpenSearchHandler{
			serviceuser:   mocks.serviceUserManager,
			service:       mocks.serviceManager,
			openSearchACL: mocks.aclManager,
			secretHandler: secret.Handler{
				Project:     mocks.projectManager,
				ProjectName: projectName,
			},
			projectName: projectName,
		}
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)

	})

	AfterEach(func() {
		cancel()
		logger = nil
		//		applicationBuilder = aiven_nais_io_v1.AivenApplicationBuilder{}
		ctx = nil
		sharedSecret = corev1.Secret{}
		cancel = nil
		opensearchHandler = OpenSearchHandler{}

		application = aiven_nais_io_v1.AivenApplication{}
		//	var individualSecret corev1.Secret

	})
	When("it receives a spec without OpenSearch", func() {
		BeforeEach(func() {
		})

		It("doesn't crash", func() {
			individualSecrets, err := opensearchHandler.Apply(ctx, &application, &sharedSecret, logger)
			Expect(err).To(Succeed())
			Expect(sharedSecret).To(Equal(corev1.Secret{}))
			Expect(individualSecrets).To(BeNil())
		})

	})

	When("it receives a spec with OpenSearch requested", func() {
		Context("and the service is unavailable", func() {
			BeforeEach(func() {
				application = applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
							Instance: serviceName,
							Access:   access,
						},
					}).
					Build()
				sharedSecret = corev1.Secret{}
				defaultServiceManagerMock(data)
			})

			It("sets the correct aiven fail condition", func() {
				individualSecrets, err := opensearchHandler.Apply(ctx, &application, &sharedSecret, logger)

				Expect(err).ToNot(Succeed())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())

			})
		})
		Context("and service users are unavailable", func() {
			BeforeEach(func() {
				data = testData{
					instanceName:             instance,
					serviceName:              serviceUserName,
					serviceURI:               serviceURI,
					serviceHost:              serviceHost,
					servicePort:              servicePort,
					access:                   access,
					username:                 serviceUserName,
					serviceNameAnnotationKey: "",
					serviceUserAnnotationKey: "",
					usernameKey:              "",
					passwordKey:              "",
					uriKey:                   "",
					hostKey:                  "",
					portKey:                  "",
				}

				application = applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
							Instance: instance,
							Access:   access,
						},
					}).
					Build()
				sharedSecret = corev1.Secret{}
				defaultServiceManagerMock(data)
				mocks.serviceUserManager.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, aiven.Error{
						Message:  "aiven-error",
						MoreInfo: "aiven-more-info",
						Status:   500,
					})

			})

			It("sets the correct aiven fail condition", func() {
				individualSecrets, err := opensearchHandler.Apply(ctx, &application, &sharedSecret, logger)

				Expect(err).ToNot(Succeed())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())

			})

		})
	})
	When("it receives a spec", func() {
		BeforeEach(func() {
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
						Instance: instance,
						Access:   access,
					},
				}).
				Build()
			sharedSecret = corev1.Secret{}

			mocks.serviceManager.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
				Return(&service.ServiceAddresses{
					ServiceURI: serviceURI,
					OpenSearch: service.ServiceAddress{
						URI:  serviceURI,
						Host: serviceHost,
						Port: servicePort,
					},
				}, nil)

		})
		Context("and the service user already exists", func() {
			BeforeEach(func() {
				mocks.serviceUserManager.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(&aiven.ServiceUser{
						Username: serviceUserName,
						Password: servicePassword,
					}, nil)
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return("my-ca", nil)

				application = applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
							Instance:   instance,
							Access:     access,
							SecretName: "foo",
						},
					}).
					Build()
				sharedSecret = corev1.Secret{}

			})

			It("Uses the existing user", func() {
				individualSecrets, err := opensearchHandler.Apply(ctx, &application, &sharedSecret, logger)

				Expect(err).To(BeNil())
				expected := []corev1.Secret{
					{
						TypeMeta: metav1.TypeMeta{Kind: "", APIVersion: ""},
						ObjectMeta: metav1.ObjectMeta{
							Name:                       "foo",
							GenerateName:               "",
							Namespace:                  "",
							SelfLink:                   "",
							UID:                        "",
							ResourceVersion:            "",
							Generation:                 0,
							DeletionTimestamp:          nil,
							DeletionGracePeriodSeconds: nil,
							Labels: map[string]string{
								"type":                              "aivenator.aiven.nais.io",
								"app":                               "",
								"team":                              "",
								"aiven.nais.io/secret-generation":   "0",
								"aivenator.aiven.nais.io/protected": "false",
							},
							Annotations: map[string]string{
								"opensearch.aiven.nais.io/serviceName": "my-instance",
								"opensearch.aiven.nais.io/project":     "my-project",
								"nais.io/deploymentCorrelationID":      "",
								"aivenator.aiven.nais.io/protected":    "false",
								"opensearch.aiven.nais.io/serviceUser": "team-a",
							},
							OwnerReferences: nil,
							Finalizers:      []string{constants.AivenatorFinalizer},
							ManagedFields:   nil,
						},
						Immutable:  nil,
						Data:       nil,
						StringData: nil,
						Type:       "",
					},
				}
				individualSecrets[0].StringData = nil
				Expect(individualSecrets).To(Equal(expected))
			})
		})

		Context("and the service user doesn't exist", func() {
			BeforeEach(func() {
				defaultServiceManagerMock(data)

				mocks.serviceUserManager.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, aiven.Error{
					Message: "Service user does not exist", Status: 404})

				mocks.serviceUserManager.On("Create", mock.Anything, "-r-9Nv", "my-project", "my-instance", (*aiven.AccessControl)(nil), mock.Anything).Return(&aiven.ServiceUser{
					Username: data.username,
					Password: servicePassword,
				}, nil)
				mocks.aclManager.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(&aiven.OpenSearchACLResponse{
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
				mocks.aclManager.On("Update", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
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
			})

			It("Creates and returns creds for the new user", func() {
				sharedSecret := &corev1.Secret{}
				individualSecrets, err := opensearchHandler.Apply(ctx, &application, sharedSecret, logger)

				Expect(err).ToNot(HaveOccurred())
				Expect(sharedSecret.StringData[OpenSearchUser]).To(Equal(serviceUserName))
				Expect(individualSecrets).To(BeNil())

			})

		})
	})
	When("it receives a spec with individual secret instance", func() {
		BeforeEach(func() {
			mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return("my-ca", nil)
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
						Instance:   instance,
						Access:     access,
						SecretName: "foo",
					},
				}).
				Build()
			sharedSecret = corev1.Secret{}

			mocks.serviceManager.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
				Return(&service.ServiceAddresses{
					ServiceURI: serviceURI,
					OpenSearch: service.ServiceAddress{
						URI:  serviceURI,
						Host: serviceHost,
						Port: servicePort,
					},
				}, nil)

		})
		Context("and the service user already exists", func() {
			BeforeEach(func() {
				mocks.serviceUserManager.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(&aiven.ServiceUser{
						Username: serviceUserName,
						Password: servicePassword,
					}, nil)
			})
			It("uses the existing user", func() {
				individualSecrets, err := opensearchHandler.Apply(ctx, &application, &sharedSecret, logger)

				Expect(err).To(BeNil())
				expected := corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      application.Spec.OpenSearch.SecretName,
						Namespace: application.GetNamespace(),
						Annotations: map[string]string{
							ProjectAnnotation:                 projectName,
							ServiceNameAnnotation:             instance,
							ServiceUserAnnotation:             serviceUserName,
							constants.AivenatorProtectedKey:   "false",
							"nais.io/deploymentCorrelationID": "",
						},
						Labels:     individualSecrets[0].ObjectMeta.Labels,
						Finalizers: []string{constants.AivenatorFinalizer},
					},
					// Check these individually
					Data:       individualSecrets[0].Data,
					StringData: individualSecrets[0].StringData,
				}
				Expect(sharedSecret.StringData).To(HaveLen(0))
				Expect(individualSecrets).To(HaveLen(1))
				Expect(individualSecrets[0]).To(Equal(expected))
				Expect(utils.KeysFromStringMap(individualSecrets[0].StringData)).To(ConsistOf(
					OpenSearchUser, OpenSearchPassword, OpenSearchURI, OpenSearchHost, OpenSearchPort, secret.AivenCAKey, secret.AivenSecretUpdatedKey,
				))
			})
		})
	})
})
