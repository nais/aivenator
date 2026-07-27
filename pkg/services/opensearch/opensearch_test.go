package opensearch

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/constants"
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
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	serviceName     = "opensearch-my-namespace-my-instance"
	secretName      = "foo"
	// legacySeededUsername uses the pre-CR base64 suffix alphabet (uppercase,
	// '_'): invalid as a CR name, so Apply must mint a fresh username instead of adopting it.
	legacySeededUsername = "team-a-r-3D_"
	access               = "read"
	testNamespace        = "my-namespace"
)

// mintedNameShape pins a freshly minted username to the target scheme for this
// suite's app ("test-app") and read access: test-app-r-<h1>-<h2>-<YYYYwWW>.
var mintedNameShape = regexp.MustCompile(`^test-app-r-[0-9a-f]{6}-[0-9a-f]{5}-[0-9]{4}w[0-9]{2}$`)

type mockContainer struct {
	aclManager         *operator.MockOpenSearchACLManager
	crServiceUser      *operator.MockServiceUserManager
	projectManager     *project.MockProjectManager
	serviceManager     *service.MockServiceManager
	serviceUserManager *serviceuser.MockServiceUserManager
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

	serviceUserSecret := map[string]string{
		operator.ServiceUserUsername: serviceUserName,
		operator.ServiceUserPassword: servicePassword,
		operator.ServiceUserHost:     serviceHost,
		operator.ServiceUserPort:     strconv.Itoa(servicePort),
	}

	BeforeEach(func() {
		root := log.New()
		root.Out = GinkgoWriter
		logger = log.NewEntry(root)
		applicationBuilder = aiven_nais_io_v1.NewAivenApplicationBuilder("test-app", testNamespace)
		mocks = mockContainer{
			aclManager:         operator.NewMockOpenSearchACLManager(GinkgoT()),
			crServiceUser:      operator.NewMockServiceUserManager(GinkgoT()),
			projectManager:     project.NewMockProjectManager(GinkgoT()),
			serviceManager:     service.NewMockServiceManager(GinkgoT()),
			serviceUserManager: serviceuser.NewMockServiceUserManager(GinkgoT()),
		}

		scheme := runtime.NewScheme()
		Expect(aiven_io_v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			&aiven_io_v1alpha1.OpenSearch{ObjectMeta: metav1.ObjectMeta{Name: serviceName, Namespace: testNamespace}, Status: aiven_io_v1alpha1.OpenSearchStatus{State: utils.ReadyState}},
			// Seed the app-facing secret with a legacy (pre-CR, RFC-1123-invalid)
			// username, so the existing specs exercise the legacy-tracking flow.
			// Valid-name adoption and fresh minting use their own secrets.
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: testNamespace, Annotations: map[string]string{ServiceUserAnnotation: legacySeededUsername}}},
		).Build()

		opensearchHandler = OpenSearchHandler{
			crServiceUser: mocks.crServiceUser,
			k8sReader:     fakeClient,
			openSearchACL: mocks.aclManager,
			projectName:   projectName,
			service:       mocks.serviceManager,
			serviceuser:   mocks.serviceUserManager,
			secretConfig: utils.SecretConfig{
				Project:     mocks.projectManager,
				ProjectName: projectName,
			},
		}
		addressesMock := service.MockServiceAddresses{}
		addressesMock.On("OpenSearch").Return(service.ServiceAddress{
			URI:  serviceURI,
			Host: serviceHost,
			Port: servicePort,
		}).Maybe()
		addressesMock.On("OpenSearchDashboard").Return(service.ServiceAddress{
			URI:  serviceURI,
			Host: serviceHost,
			Port: servicePort,
		}).Maybe()
		opensearchServiceAddresses = &addressesMock

		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	})
	AfterEach(func() {
		cancel()
	})

	mockAivenReturnOpensearchGetOk := func() {
		mocks.serviceManager.On("GetServiceAddresses", mock.Anything, mock.Anything, mock.Anything).
			Return(opensearchServiceAddresses, nil)
	}
	mockAivenReturnCaOk := func() {
		mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return("my-ca", nil)
	}
	mockCreateServiceUserOk := func() {
		mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&operator.ServiceUser{
				Username: serviceUserName,
				Secret:   serviceUserSecret,
			}, nil)
	}
	mockCreateACLsOk := func() {
		mocks.aclManager.On("CreateServiceUserACLs", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
	}

	When("it receives a spec without OpenSearch", func() {
		It("doesn't crash", func() {
			individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)
			Expect(err).To(Succeed())
			Expect(individualSecrets).To(BeNil())
		})
	})

	When("it receives a spec with OpenSearch requested", func() {
		BeforeEach(func() {
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
						Instance:   instance,
						Access:     access,
						SecretName: secretName,
					},
				}).
				Build()
		})

		Context("and the service is unavailable", func() {
			BeforeEach(func() {
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

		// A transient (non-NotFound) failure reading the existing secret must not be
		// mistaken for "no prior user" — that would silently mint a second, orphaned
		// service user under a different name instead of surfacing a retryable error.
		Context("and reading the existing secret fails with a non-NotFound error", func() {
			BeforeEach(func() {
				opensearchHandler.k8sReader = erroringReader{
					Reader:  opensearchHandler.k8sReader,
					failKey: client.ObjectKey{Namespace: testNamespace, Name: secretName},
					err:     errors.New("api server down"),
				}
				mockAivenReturnOpensearchGetOk()
			})
			It("fails instead of silently minting a new service user", func() {
				individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)

				Expect(err).To(HaveOccurred())
				Expect(individualSecrets).To(BeNil())
			})
		})

		Context("and the service user cannot be provisioned", func() {
			BeforeEach(func() {
				mockAivenReturnOpensearchGetOk()
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errors.New("failed to provision"))
			})
			It("sets the correct aiven fail condition", func() {
				individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)

				Expect(err).ToNot(Succeed())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
			})
		})

		Context("and updating ACLs fails", func() {
			BeforeEach(func() {
				mockAivenReturnOpensearchGetOk()
				mockCreateServiceUserOk()
				mocks.aclManager.On("CreateServiceUserACLs", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(errors.New("acl failure"))
			})
			It("sets the correct aiven fail condition", func() {
				individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)

				Expect(err).ToNot(Succeed())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
			})
		})

		// The operator publishes the ServiceUser secret; a key missing from it
		// (operator not done, or a contract drift) must fail loudly, not silently
		// project an empty credential.
		Context("and the operator secret is missing a required key", func() {
			BeforeEach(func() {
				mockAivenReturnOpensearchGetOk()
				mockCreateACLsOk()
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(&operator.ServiceUser{
						Username: serviceUserName,
						Secret:   map[string]string{operator.ServiceUserHost: serviceHost, operator.ServiceUserPort: strconv.Itoa(servicePort)},
					}, nil)
			})
			It("sets the correct aiven fail condition", func() {
				individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)

				Expect(err).ToNot(Succeed())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
			})
		})

		Context("and the project CA cannot be fetched", func() {
			BeforeEach(func() {
				mockAivenReturnOpensearchGetOk()
				mockCreateServiceUserOk()
				mockCreateACLsOk()
				mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return("", aiven.Error{Message: "boom", Status: 500})
			})
			It("sets the correct aiven fail condition", func() {
				individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)

				Expect(err).ToNot(Succeed())
				Expect(application.Status.GetConditionOfType(aiven_nais_io_v1.AivenApplicationAivenFailure)).ToNot(BeNil())
				Expect(individualSecrets).To(BeNil())
			})
		})

		Context("and the existing secret carries a legacy (pre-CR) username", func() {
			BeforeEach(func() {
				mockAivenReturnOpensearchGetOk()
				mockAivenReturnCaOk()
				mockCreateACLsOk()
			})

			It("mints a fresh username and tracks the legacy one", func() {
				const mintedUsername = "test-app-r-a1b2c3-d4e5f-2026w30"

				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.MatchedBy(func(spec operator.ServiceUserSpec) bool {
					return mintedNameShape.MatchString(spec.Name) && spec.ServiceName == serviceName && spec.Project == projectName && spec.Namespace == testNamespace
				}), mock.Anything).
					Return(&operator.ServiceUser{
						Username: mintedUsername,
						Secret:   serviceUserSecret,
					}, nil)

				individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)

				Expect(err).To(BeNil())
				Expect(individualSecrets).To(HaveLen(1))
				annotations := individualSecrets[0].GetAnnotations()
				Expect(annotations).To(HaveKeyWithValue(ServiceNameAnnotation, serviceName))
				Expect(annotations).To(HaveKeyWithValue(ProjectAnnotation, projectName))
				Expect(annotations).To(HaveKeyWithValue(ServiceUserAnnotation, mintedUsername))
				Expect(annotations).To(HaveKeyWithValue(LegacyServiceUserAnnotation, legacySeededUsername))
				Expect(individualSecrets[0].Finalizers).To(ContainElement(constants.AivenatorFinalizer))
			})

			It("fills the secret with the connection details", func() {
				mockCreateServiceUserOk()

				individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)

				Expect(err).To(BeNil())
				Expect(individualSecrets).To(HaveLen(1))
				Expect(individualSecrets[0].StringData).To(HaveKeyWithValue(OpenSearchUser, serviceUserName))
				Expect(individualSecrets[0].StringData).To(HaveKeyWithValue(OpenSearchPassword, servicePassword))
				Expect(individualSecrets[0].StringData).To(HaveKeyWithValue(OpenSearchURI, "https://example.com:1234"))
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

		Context("and the existing secret carries a valid CR-mode username", func() {
			BeforeEach(func() {
				application = applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
							Instance:   instance,
							Access:     access,
							SecretName: "adopted-secret",
						},
					}).
					Build()
				existing := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name: "adopted-secret", Namespace: testNamespace,
					Annotations: map[string]string{ServiceUserAnnotation: serviceUserName},
				}}
				Expect(opensearchHandler.k8sReader.(client.Client).Create(ctx, existing)).To(Succeed())
				mocks.crServiceUser.On("ServiceName", mock.Anything, testNamespace, serviceUserName).Return(serviceName, true, nil)
				mockAivenReturnOpensearchGetOk()
				mockAivenReturnCaOk()
				mockCreateACLsOk()
			})

			It("adopts the existing username", func() {
				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.MatchedBy(func(spec operator.ServiceUserSpec) bool {
					return spec.Name == serviceUserName && spec.ServiceName == serviceName && spec.Project == projectName && spec.Namespace == testNamespace
				}), mock.Anything).
					Return(&operator.ServiceUser{
						Username: serviceUserName,
						Secret:   serviceUserSecret,
					}, nil)

				individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)

				Expect(err).To(BeNil())
				Expect(individualSecrets).To(HaveLen(1))
				annotations := individualSecrets[0].GetAnnotations()
				Expect(annotations).To(HaveKeyWithValue(ServiceUserAnnotation, serviceUserName))
				Expect(annotations).ToNot(HaveKey(LegacyServiceUserAnnotation))
			})
		})

		Context("and the existing secret's username has a CR targeting another service", func() {
			// Switching spec.opensearch.instance without rotating the secret name is
			// illegal: the annotated CR is bound to the old instance's service
			// (immutable), so provisioning must fail rather than mint a colliding user.
			BeforeEach(func() {
				application = applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
							Instance:   instance,
							Access:     access,
							SecretName: "adopted-secret",
						},
					}).
					Build()
				existing := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name: "adopted-secret", Namespace: testNamespace,
					Annotations: map[string]string{ServiceUserAnnotation: serviceUserName},
				}}
				Expect(opensearchHandler.k8sReader.(client.Client).Create(ctx, existing)).To(Succeed())
				mocks.crServiceUser.On("ServiceName", mock.Anything, testNamespace, serviceUserName).Return("opensearch-my-namespace-old-instance", true, nil)
				mockAivenReturnOpensearchGetOk()
			})

			It("fails instead of re-pointing the CR or minting", func() {
				individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)

				Expect(errors.Is(err, utils.ErrUnrecoverable)).To(BeTrue())
				Expect(individualSecrets).To(BeNil())
			})
		})

		Context("and the app-facing secret does not yet exist", func() {
			BeforeEach(func() {
				application = applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
							Instance:   instance,
							Access:     access,
							SecretName: "new-secret",
						},
					}).
					Build()
				mockAivenReturnOpensearchGetOk()
				mockAivenReturnCaOk()
				mockCreateACLsOk()
			})

			It("mints a username in the target scheme and marks the secret", func() {
				const mintedName = "test-app-r-a1b2c3-d4e5f-2026w30"

				mocks.crServiceUser.On("CreateServiceUser", mock.Anything, mock.Anything, mock.MatchedBy(func(spec operator.ServiceUserSpec) bool {
					return mintedNameShape.MatchString(spec.Name)
				}), mock.Anything).
					Return(&operator.ServiceUser{
						Username: mintedName,
						Secret:   serviceUserSecret,
					}, nil)

				individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)
				Expect(err).ToNot(HaveOccurred())
				Expect(individualSecrets).To(HaveLen(1))
				Expect(individualSecrets[0].GetAnnotations()).To(HaveKeyWithValue(ServiceUserAnnotation, mintedName))
			})
		})

		Context("and the service user has no specified OpenSearch access", func() {
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
				mockAivenReturnOpensearchGetOk()
				mockAivenReturnCaOk()
				mockCreateServiceUserOk()
			})
			It("the service user receives default ACLs", func() {
				mocks.aclManager.On("CreateServiceUserACLs", mock.Anything, mock.Anything, mock.MatchedBy(func(spec operator.OpenSearchACLSpec) bool {
					return spec.Access == DefaultACLAccess && spec.Username == serviceUserName && spec.ServiceName == serviceName
				}), mock.Anything).Return(nil)

				individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)

				Expect(err).To(BeNil())
				Expect(individualSecrets).To(HaveLen(1))
				Expect(application.Spec.OpenSearch.Access).To(Equal(DefaultACLAccess))
			})
		})

		// Backwards compatibility: apps from before the "opensearch-<ns>-<instance>"
		// naming convention have spec.Instance set to the already-existing, full
		// service name directly. Apply must still find their CR.
		Context("and the CR predates the namespaced naming convention", func() {
			const legacyServiceName = "legacy-opensearch-instance"

			BeforeEach(func() {
				application = applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
							Instance:   legacyServiceName,
							Access:     access,
							SecretName: secretName,
						},
					}).
					Build()
				cr := &aiven_io_v1alpha1.OpenSearch{
					ObjectMeta: metav1.ObjectMeta{Name: legacyServiceName, Namespace: testNamespace},
					Status:     aiven_io_v1alpha1.OpenSearchStatus{State: utils.ReadyState},
				}
				Expect(opensearchHandler.k8sReader.(client.Client).Create(ctx, cr)).To(Succeed())
				mockAivenReturnCaOk()
				mockCreateServiceUserOk()
			})

			It("falls back to the instance value as the service name", func() {
				mocks.serviceManager.On("GetServiceAddresses", mock.Anything, projectName, legacyServiceName).
					Return(opensearchServiceAddresses, nil)
				mocks.aclManager.On("CreateServiceUserACLs", mock.Anything, mock.Anything, mock.MatchedBy(func(spec operator.OpenSearchACLSpec) bool {
					return spec.ServiceName == legacyServiceName
				}), mock.Anything).Return(nil)

				individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)

				Expect(err).To(Succeed())
				Expect(individualSecrets).To(HaveLen(1))
				Expect(individualSecrets[0].GetAnnotations()).To(HaveKeyWithValue(ServiceNameAnnotation, legacyServiceName))
			})
		})

		// Regression guard: once the namespaced CR is known to exist (even if not
		// ready), Apply must not silently fall back to a same-named legacy CR.
		Context("and a namespaced CR exists but is not ready, while a same-named legacy CR would also match", func() {
			const collidingInstance = "collided-legacy"

			BeforeEach(func() {
				notReady := &aiven_io_v1alpha1.OpenSearch{
					ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("opensearch-%s-%s", testNamespace, collidingInstance), Namespace: testNamespace},
					Status:     aiven_io_v1alpha1.OpenSearchStatus{State: "NOT_RUNNING"},
				}
				Expect(opensearchHandler.k8sReader.(client.Client).Create(ctx, notReady)).To(Succeed())
				decoy := &aiven_io_v1alpha1.OpenSearch{
					ObjectMeta: metav1.ObjectMeta{Name: collidingInstance, Namespace: testNamespace},
					Status:     aiven_io_v1alpha1.OpenSearchStatus{State: utils.ReadyState},
				}
				Expect(opensearchHandler.k8sReader.(client.Client).Create(ctx, decoy)).To(Succeed())

				application = applicationBuilder.
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
							Instance:   collidingInstance,
							Access:     access,
							SecretName: secretName,
						},
					}).
					Build()
			})

			It("rejects on the not-ready namespaced CR instead of falling back to the decoy", func() {
				individualSecrets, err := opensearchHandler.Apply(ctx, &application, logger)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("expected RUNNING"))
				Expect(individualSecrets).To(BeNil())
			})
		})
	})

	// Security: cross-namespace access is rejected.
	// The service name is derived from the requesting namespace, so a spec
	// naming another team's instance resolves to a CR that cannot exist here.
	When("Apply is called without a matching OpenSearch CR in the requesting namespace", func() {
		var attackerApp aiven_nais_io_v1.AivenApplication

		BeforeEach(func() {
			attackerApp = aiven_nais_io_v1.NewAivenApplicationBuilder("evil-app", "attacker-ns").
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
						Instance:   instance,
						Access:     "admin",
						SecretName: "stolen-creds",
					},
				}).
				Build()
			// Mocked to succeed — if the namespace check regresses, Apply() would succeed and the assertion catches it.
			mocks.projectManager.On("GetCA", mock.Anything, mock.Anything).Return("my-ca", nil).Maybe()
			mocks.serviceManager.On("GetServiceAddresses", mock.Anything, projectName, mock.Anything).
				Return(opensearchServiceAddresses, nil).Maybe()
		})

		It("returns an error because no ownership validation passes", func() {
			individualSecrets, err := opensearchHandler.Apply(ctx, &attackerApp, logger)
			Expect(err).To(HaveOccurred(), "Apply() should reject when no OpenSearch CR exists in namespace")
			Expect(individualSecrets).To(BeNil())
		})
	})

	When("no OpenSearch CR exists in namespace for the instance", func() {
		BeforeEach(func() {
			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
						Instance:   "nonexistent",
						Access:     access,
						SecretName: secretName,
					},
				}).
				Build()
		})

		It("returns ErrNotFound", func() {
			_, err := opensearchHandler.Apply(ctx, &application, logger)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found in namespace"))
			Expect(err.Error()).To(ContainSubstring("nonexistent"))
		})
	})

	// Namespace naming collision: opensearch-<ns>-<instance> is ambiguous.
	// e.g. namespace "a" + instance "b-foo" and namespace "a-b" + instance "foo"
	// both produce "opensearch-a-b-foo". Aiven rejects the duplicate, so the
	// colliding CR exists in-cluster but never reaches RUNNING.
	When("the OpenSearch CR exists but is NOT in RUNNING state (naming collision)", func() {
		BeforeEach(func() {
			cr := &aiven_io_v1alpha1.OpenSearch{
				ObjectMeta: metav1.ObjectMeta{Name: "opensearch-" + testNamespace + "-collided", Namespace: testNamespace},
				Status:     aiven_io_v1alpha1.OpenSearchStatus{State: "NOT_RUNNING"},
			}
			Expect(opensearchHandler.k8sReader.(client.Client).Create(ctx, cr)).To(Succeed())

			application = applicationBuilder.
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
					OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
						Instance:   "collided",
						Access:     access,
						SecretName: secretName,
					},
				}).
				Build()
		})

		It("rejects because the instance is not RUNNING", func() {
			_, err := opensearchHandler.Apply(ctx, &application, logger)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("expected RUNNING"))
		})
	})

	When("Cleanup is called", func() {
		var secret corev1.Secret

		BeforeEach(func() {
			secret = corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: testNamespace,
					Annotations: map[string]string{
						ServiceNameAnnotation: serviceName,
						ServiceUserAnnotation: serviceUserName,
						ProjectAnnotation:     projectName,
					},
				},
			}
		})

		Context("for a secret backed by a ServiceUser CR (new mode)", func() {
			BeforeEach(func() {
				mocks.crServiceUser.On("Exists", mock.Anything, testNamespace, serviceUserName).Return(true, nil)
			})

			It("removes the ACL entry via the CR and deletes the ServiceUser CR", func() {
				mocks.aclManager.On("DeleteServiceUserACLs", mock.Anything, testNamespace, serviceName, serviceUserName, mock.Anything).Return(nil)
				mocks.crServiceUser.On("DeleteServiceUser", mock.Anything, testNamespace, serviceUserName, mock.Anything).Return(nil)

				Expect(opensearchHandler.Cleanup(ctx, &secret, logger)).To(Succeed())
			})

			// A k8s NotFound from the CR delete is propagated, not tolerated.
			It("propagates a k8s NotFound from the CR delete", func() {
				mocks.aclManager.On("DeleteServiceUserACLs", mock.Anything, testNamespace, serviceName, serviceUserName, mock.Anything).Return(nil)
				mocks.crServiceUser.On("DeleteServiceUser", mock.Anything, testNamespace, serviceUserName, mock.Anything).
					Return(k8serrors.NewNotFound(schema.GroupResource{Group: "aiven.io", Resource: "serviceusers"}, serviceUserName))

				Expect(opensearchHandler.Cleanup(ctx, &secret, logger)).ToNot(Succeed())
			})

			It("also deletes a tracked legacy user", func() {
				const legacyUsername = "team-a-r-3D_"
				secret.Annotations[LegacyServiceUserAnnotation] = legacyUsername
				mocks.aclManager.On("DeleteServiceUserACLs", mock.Anything, testNamespace, serviceName, serviceUserName, mock.Anything).Return(nil)
				mocks.crServiceUser.On("DeleteServiceUser", mock.Anything, testNamespace, serviceUserName, mock.Anything).Return(nil)
				mocks.aclManager.On("DeleteServiceUserACLs", mock.Anything, testNamespace, serviceName, legacyUsername, mock.Anything).Return(nil)
				mocks.serviceUserManager.On("Delete", mock.Anything, legacyUsername, projectName, serviceName, mock.Anything).Return(nil)

				Expect(opensearchHandler.Cleanup(ctx, &secret, logger)).To(Succeed())
			})
		})

		Context("for a pre-migration secret (old mode, no CR marker)", func() {
			It("removes the ACL entry via the CR but deletes the user via the direct API", func() {
				mocks.crServiceUser.On("Exists", mock.Anything, testNamespace, serviceUserName).Return(false, nil)
				mocks.aclManager.On("DeleteServiceUserACLs", mock.Anything, testNamespace, serviceName, serviceUserName, mock.Anything).Return(nil)
				mocks.serviceUserManager.On("Delete", mock.Anything, serviceUserName, projectName, serviceName, mock.Anything).Return(nil)

				Expect(opensearchHandler.Cleanup(ctx, &secret, logger)).To(Succeed())
			})

			It("returns the error when the direct-API delete fails", func() {
				mocks.crServiceUser.On("Exists", mock.Anything, testNamespace, serviceUserName).Return(false, nil)
				mocks.aclManager.On("DeleteServiceUserACLs", mock.Anything, testNamespace, serviceName, serviceUserName, mock.Anything).Return(nil)
				mocks.serviceUserManager.On("Delete", mock.Anything, serviceUserName, projectName, serviceName, mock.Anything).Return(aiven.Error{Message: "boom", Status: 500})

				Expect(opensearchHandler.Cleanup(ctx, &secret, logger)).ToNot(Succeed())
			})
		})

		Context("for a secret without OpenSearch annotations", func() {
			It("is a no-op", func() {
				secret.Annotations = map[string]string{}
				Expect(opensearchHandler.Cleanup(ctx, &secret, logger)).To(Succeed())
			})
		})
	})
})

// erroringReader wraps a client.Reader, injecting err for Get calls against
// failKey while delegating everything else, so one specific read can fail
// (e.g. the app-facing secret) without breaking unrelated lookups (e.g.
// resolving the OpenSearch CR).
type erroringReader struct {
	client.Reader
	failKey client.ObjectKey
	err     error
}

func (r erroringReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if key == r.failKey {
		return r.err
	}
	return r.Reader.Get(ctx, key, obj, opts...)
}

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
