package opensearch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/constants"
	thirdparty_aiven "github.com/nais/aivenator/internal/thirdparty/aiven"
	"github.com/nais/aivenator/pkg/aiven/opensearch"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	serviceUserName = "team-a"
	servicePassword = "service-password"
	projectName     = "my-project"
	serviceURI      = "http://example.com:1234"
	serviceHost     = "example.com"
	servicePort     = 1234
	instance        = "my-instance"
	serviceName     = "my-service"
	secretName      = "foo"
	access          = "read"
	testNamespace   = "my-namespace"
)

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
	var logger log.FieldLogger
	var applicationBuilder aiven_nais_io_v1.AivenApplicationBuilder
	var ctx context.Context
	var cancel context.CancelFunc
	var opensearchHandler OpenSearchHandler
	var application aiven_nais_io_v1.AivenApplication
	var opensearchServiceAddresses service.ServiceAddresses

	BeforeEach(func() {
		root := log.New()
		root.Out = GinkgoWriter
		logger = log.NewEntry(root)
		applicationBuilder = aiven_nais_io_v1.NewAivenApplicationBuilder("test-app", testNamespace)
		mocks = mockContainer{
			serviceUserManager: serviceuser.NewMockServiceUserManager(GinkgoT()),
			serviceManager:     service.NewMockServiceManager(GinkgoT()),
			projectManager:     project.NewMockProjectManager(GinkgoT()),
			aclManager:         opensearch.NewMockACLManager(GinkgoT()),
		}

		scheme := runtime.NewScheme()
		Expect(thirdparty_aiven.AddToScheme(scheme)).To(Succeed())
		// Pre-populate CRs matching test constants in testNamespace.
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&thirdparty_aiven.OpenSearch{ObjectMeta: metav1.ObjectMeta{Name: serviceName, Namespace: testNamespace}},
			&thirdparty_aiven.OpenSearch{ObjectMeta: metav1.ObjectMeta{Name: instance, Namespace: testNamespace}},
		).Build()

		opensearchHandler = OpenSearchHandler{
			serviceuser:   mocks.serviceUserManager,
			service:       mocks.serviceManager,
			openSearchACL: mocks.aclManager,
			secretConfig: utils.SecretConfig{
				Project:     mocks.projectManager,
				ProjectName: projectName,
			},
			projectName: projectName,
			k8sClient:   fakeClient,
		}
		mock := service.MockServiceAddresses{}
		mock.EXPECT().OpenSearch().Return(service.ServiceAddress{
			URI:  serviceURI,
			Host: serviceHost,
			Port: servicePort,
		})
		mock.EXPECT().OpenSearchDashboard().Return(service.ServiceAddress{
			URI:  serviceURI,
			Host: serviceHost,
			Port: servicePort,
		})
		opensearchServiceAddresses = &mock

		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	})
	AfterEach(func() {
		cancel()
	})

	When("it receives a spec without OpenSearch", func() {
		It("doesn't crash", func() {
			individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)
			Expect(err).To(Succeed())
			Expect(individualSecrets).To(BeNil())
		})
	})

	mockAivenReturnOpensearchGetOk := func() {
		mocks.serviceManager.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
			Return(opensearchServiceAddresses, nil)
	}
	mockAivenReturnOpensearchGetServiceUserOk := func() {
		{
			mocks.serviceUserManager.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return(&aiven.ServiceUser{
					Username: serviceUserName,
					Password: servicePassword,
				}, nil)
		}
	}
	mockAivenReturnCaOk := func() {
		mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return("my-ca", nil)
	}
	mockAivenReturnAclManagerGetOk := func() {
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
	}
	mockAivenReturnAclManagerUpdateOk := func() {
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
	}

	When("it receives a spec with OpenSearch requested", func() {
		Context("and the service is unavailable", func() {
			BeforeEach(func() {
				application = applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
							Instance:   serviceName,
							Access:     access,
							SecretName: secretName,
						},
					}).
					Build()
				mocks.serviceManager.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, aiven.Error{
						Message:  "aiven-error",
						MoreInfo: "aiven-more-info",
						Status:   500,
					})
			})
			It("sets the correct aiven fail condition", func() {
				individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)

				Expect(err).ToNot(Succeed())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
			})
		})
		Context("and the service is not found in Aiven", func() {
			BeforeEach(func() {
				application = applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
							Instance:   serviceName,
							Access:     access,
							SecretName: secretName,
						},
					}).
					Build()
				mocks.serviceManager.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
					Return(nil, aiven.Error{
						Message:  "{\"message\":\"service not found\"}",
						MoreInfo: "aiven-more-info",
						Status:   404,
					})
			})
			It("returns a recoverable not found error", func() {
				individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)

				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, utils.ErrNotFound)).To(BeTrue())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
			})
		})
		Context("and service users are unavailable", func() {
			BeforeEach(func() {
				application = applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
							Instance:   serviceName,
							Access:     access,
							SecretName: secretName,
						},
					}).
					Build()
				mockAivenReturnOpensearchGetOk()
				mocks.serviceUserManager.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, aiven.Error{
						Message:  "aiven-error",
						MoreInfo: "aiven-more-info",
						Status:   500,
					})

				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return("my-ca", nil)
			})
			It("sets the correct aiven fail condition", func() {
				individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)

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
						Instance:   serviceName,
						Access:     access,
						SecretName: secretName,
					},
				}).
				Build()
			mockAivenReturnOpensearchGetOk()
			mockAivenReturnAclManagerGetOk()
			mockAivenReturnAclManagerUpdateOk()
		})
		Context("and the service user already exists", func() {
			BeforeEach(func() {
				mockAivenReturnOpensearchGetServiceUserOk()
				mockAivenReturnCaOk()
				application = applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
							Instance:   serviceName,
							Access:     access,
							SecretName: secretName,
						},
					}).
					Build()
			})

			It("Uses the existing user", func() {
				individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)

				Expect(err).To(BeNil())
				expected := []corev1.Secret{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      secretName,
							Namespace: testNamespace,
							Labels: map[string]string{
								"type":                              "aivenator.aiven.nais.io",
								"app":                               application.Name,
								"team":                              application.Namespace,
								"aiven.nais.io/secret-generation":   "0",
								"aivenator.aiven.nais.io/protected": "false",
							},
							Annotations: map[string]string{
								ServiceNameAnnotation:             serviceName,
								ProjectAnnotation:                 projectName,
								"nais.io/deploymentCorrelationID": "",
								constants.AivenatorProtectedKey:   "false",
								ServiceUserAnnotation:             serviceUserName,
							},
							Finalizers: []string{constants.AivenatorFinalizer},
						},
					},
				}
				individualSecrets[0].StringData = nil
				Expect(individualSecrets).To(Equal(expected))
			})
		})

		Context("and the service user doesn't exist", func() {
			BeforeEach(func() {
				mockAivenReturnOpensearchGetOk()
				mockAivenReturnCaOk()
				mocks.serviceUserManager.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, aiven.Error{
					Message: "Service user does not exist", Status: 404,
				})

				mocks.serviceUserManager.On("Create", mock.Anything, testNamespace+"-r-3D_", projectName, serviceName, (*aiven.AccessControl)(nil), mock.Anything).Return(&aiven.ServiceUser{
					Username: serviceUserName,
					Password: servicePassword,
				}, nil)
			})

			It("Creates and returns creds for the new user", func() {
				individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)

				Expect(err).ToNot(HaveOccurred())
				Expect(individualSecrets).To(Not(BeNil()))
			})
		})
	})

	When("it receives a spec with individual secret instance", func() {
		BeforeEach(func() {
			mockAivenReturnCaOk()
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
						Instance:   instance,
						Access:     access,
						SecretName: "foo",
					},
				}).
				Build()
			mockAivenReturnOpensearchGetOk()
			mockAivenReturnAclManagerGetOk()
			mockAivenReturnAclManagerUpdateOk()
		})
		Context("and the service user already exists", func() {
			BeforeEach(func() {
				mockAivenReturnOpensearchGetServiceUserOk()
			})
			It("uses the existing user", func() {
				individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)

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
						Labels:     individualSecrets[0].Labels,
						Finalizers: []string{constants.AivenatorFinalizer},
					},
					Data:       individualSecrets[0].Data,
					StringData: individualSecrets[0].StringData,
				}
				Expect(individualSecrets).To(HaveLen(1))
				Expect(individualSecrets[0]).To(Equal(expected))
				Expect(utils.KeysFromStringMap(individualSecrets[0].StringData)).To(ConsistOf(
					OpenSearchUser,
					OpenSearchPassword,
					OpenSearchURI,
					OpenSearchHost,
					OpenSearchPort,
					OpenSearchDashboardURI,
					OpenSearchDashboardHost,
					OpenSearchDashboardPort,
					utils.AivenCAKey,
					utils.AivenSecretUpdatedKey,
				))
			})
		})
	})
	// Security: cross-namespace access is rejected.
	// `resolveServiceName` scopes lookup to the requesting namespace only.
	// Aiven APIs are mocked to succeed so that if the namespace check is ever removed (regression), the test still catches it via the assertion.
	When("Apply is called without a matching OpenSearch CR in the requesting namespace", func() {
		var attackerApp aiven_nais_io_v1.AivenApplication

		BeforeEach(func() {
			attackerApp = aiven_nais_io_v1.NewAivenApplicationBuilder("evil-app", "attacker-ns").
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
						Instance:   "opensearch-" + testNamespace + "-important-data",
						Access:     "admin",
						SecretName: "stolen-creds",
					},
				}).
				Build()
			// Mocked to succeed — if namespace check regresses, Apply() would succeed and assertion catches it.
			mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return("my-ca", nil).Maybe()
			mocks.serviceManager.On("GetServiceAddresses", mock.Anything, projectName, mock.Anything).
				Return(opensearchServiceAddresses, nil).Maybe()
			mocks.serviceUserManager.On("Get", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return(&aiven.ServiceUser{Username: "attacker-ns-abc", Password: servicePassword}, nil).Maybe()
			mocks.aclManager.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(&aiven.OpenSearchACLResponse{
				OpenSearchACLConfig: aiven.OpenSearchACLConfig{Enabled: true},
			}, nil).Maybe()
			mocks.aclManager.On("Update", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return(&aiven.OpenSearchACLResponse{}, nil).Maybe()
		})

		It("returns an error because no ownership validation passes", func() {
			individualSecrets, err := opensearchHandler.Apply(ctx, &attackerApp, logger)
			Expect(err).To(HaveOccurred(), "Apply() should reject when no OpenSearch CR exists in namespace")
			Expect(individualSecrets).To(BeNil())
		})
	})

	When("it receives a spec w/individual secret instance, existing service user for secret", func() {
		BeforeEach(func() {
			mockAivenReturnCaOk()
			mockAivenReturnOpensearchGetOk()
			mockAivenReturnAclManagerGetOk()
			mockAivenReturnAclManagerUpdateOk()
		})
		Context("and the service user has no specified Opensearch ACLs", func() {
			BeforeEach(func() {
				application = applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
							Instance:   instance,
							Access:     "",
							SecretName: secretName,
						},
					}).
					Build()
				mockAivenReturnOpensearchGetServiceUserOk()
			})
			It("the service user receives default ACLs", func() {
				individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)

				Expect(err).To(BeNil())
				Expect(individualSecrets).To(HaveLen(1))
				Expect(application.Spec.OpenSearch.Access).To(Equal(DefaultACLAccess))
			})
		})
	})
})
