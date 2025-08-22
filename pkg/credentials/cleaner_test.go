package credentials

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/utils"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	"github.com/nais/liberator/pkg/scheme"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sClient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	MyAppName    = "app1"
	NotMyAppName = "app2"
	MyUser       = "user"

	UnusedSecret                        = "secret1"
	NotOurSecretTypeSecret              = "secret2"
	SecretUsedByPod                     = "secret3"
	ProtectedNotTimeLimited             = "secret4"
	UnusedSecretWithNoAnnotations       = "secret5"
	SecretBelongingToOtherApp           = "secret6"
	CurrentlyRequestedSecret            = "secret7"
	ProtectedNotExpired                 = "secret8"
	ProtectedExpired                    = "secret9"
	ProtectedTimeLimitedWithNoExpirySet = "secret10"

	MyNamespace    = "namespace"
	NotMyNamespace = "not-my-namespace"

	NotMySecretType = "other.nais.io"
)

func generateApplication() aiven_nais_io_v1.AivenApplication {
	application := aiven_nais_io_v1.NewAivenApplicationBuilder(MyAppName, MyNamespace).
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			SecretName: CurrentlyRequestedSecret,
		}).
		Build()
	application.SetLabels(map[string]string{
		constants.AppLabel: MyAppName,
	})
	return application
}

func buildJanitor(client Client, logger log.FieldLogger) *Cleaner {
	return &Cleaner{
		Client: client,
		Logger: logger,
	}
}

type secretSetup struct {
	name       string
	namespace  string
	secretType string
	appName    string
	opts       []MakeSecretOption
	wanted     bool
	reason     string
}

func TestCleaner(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cleaner Suite")
}

func generateAndRegisterKeptPodSecrets(clientBuilder *fake.ClientBuilder) []secretSetup {
	secrets := []secretSetup{
		{UnusedSecret, NotMyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{}, true, "Secret in another namespace should be kept"},
		{NotOurSecretTypeSecret, MyNamespace, NotMySecretType, MyAppName, []MakeSecretOption{}, true, "Unrelated secret should be kept"},
		{SecretUsedByPod, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{}, true, "Used secret should be kept"},
		{ProtectedNotTimeLimited, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{SecretIsProtected}, true, "Protected secret should be kept"},
		{SecretBelongingToOtherApp, MyNamespace, constants.AivenatorSecretType, NotMyAppName, []MakeSecretOption{}, true, "Secret belonging to different app should be kept"},
		{CurrentlyRequestedSecret, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{}, true, "Secret currently requested should be kept"},
		{ProtectedNotExpired, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{SecretIsProtected, SecretHasTimeLimit, SecretExpiresAt(time.Now().Add(48 * time.Hour))}, true, "Protected secret with time-limit that isn't expired should be kept"},
		{ProtectedTimeLimitedWithNoExpirySet, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{SecretIsProtected, SecretHasTimeLimit}, true, "Protected secret with time-limit but missing expires date should be kept"},
	}
	for _, s := range secrets {
		clientBuilder.WithRuntimeObjects(makeSecret(s.name, s.namespace, s.secretType, s.appName, s.opts...))
	}
	return secrets
}

func generateAndRegisterDeletedPodSecrets(clientBuilder *fake.ClientBuilder) []secretSetup {
	secrets := []secretSetup{
		{UnusedSecret, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{}, false, "Unused secret should be deleted"},
		{UnusedSecretWithNoAnnotations, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{SecretHasNoAnnotations}, false, "Unused secret should be deleted, even if annotations are nil"},
		{ProtectedExpired, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{SecretIsProtected, SecretHasTimeLimit, SecretExpiresAt(time.Now().Add(-48 * time.Hour))}, false, "Protected secret with time-limit that is expired should be deleted"},
	}
	for _, s := range secrets {
		clientBuilder.WithRuntimeObjects(makeSecret(s.name, s.namespace, s.secretType, s.appName, s.opts...))
	}
	return secrets
}

var _ = Describe("cleaner", func() {
	var (
		logger        log.FieldLogger
		ctx           context.Context
		clientBuilder *fake.ClientBuilder
		secrets       []secretSetup
		application   aiven_nais_io_v1.AivenApplication
	)

	type interaction struct {
		method     string
		arguments  []any
		returnArgs []any
		runFunc    func(arguments mock.Arguments)
	}

	newJanitorWithInteractions := func(interactions []interaction) (*Cleaner, *MockClient) {
		mockClient := &MockClient{}
		for _, i := range interactions {
			call := mockClient.On(i.method, i.arguments...).Return(i.returnArgs...)
			if i.runFunc != nil {
				call.Run(i.runFunc)
			}
		}
		mockClient.On("Scheme").Maybe().Return(&runtime.Scheme{})

		root := log.New()
		root.Out = GinkgoWriter
		logger = log.NewEntry(root)
		ctx = context.Background()

		return buildJanitor(mockClient, logger), mockClient
	}

	BeforeEach(func() {
		root := log.New()
		root.Out = GinkgoWriter
		logger = log.NewEntry(root)
		ctx = context.Background()
		clientBuilder = fake.NewClientBuilder()
		s := runtime.NewScheme()
		scheme.AddAll(s)
		clientBuilder.WithScheme(s)
	})

	When("no secrets exist", func() {
		It("doesn't fail", func() {
			clientBuilder.WithRuntimeObjects(
				makeSecret(UnusedSecret, NotMyNamespace, constants.AivenatorSecretType, NotMyAppName),
				makeSecret(NotOurSecretTypeSecret, NotMyNamespace, constants.AivenatorSecretType, MyAppName),
			)
			client := clientBuilder.Build()
			janitor := buildJanitor(client, logger)
			application := aiven_nais_io_v1.NewAivenApplicationBuilder(MyAppName, MyNamespace).Build()

			err := janitor.CleanUnusedSecretsForApplication(ctx, application)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	When("aivenator `sharedsecret`s", func() {
		Context("that are supposed to be kept", func() {
			BeforeEach(func() {
				secrets = generateAndRegisterKeptPodSecrets(clientBuilder)
				application = generateApplication()
			})
			It("should succeed, when the secrets are mounted as SecretVolume", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretVolume(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).ToNot(HaveOccurred())
				}
			})
			It("should succeed, when the secrets are mounted as SecretValueFrom", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretValueFrom(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).ToNot(HaveOccurred())
				}
			})
			It("should succeed, when the secrets are mounted as SecretEnvFrom", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretEnvFrom(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).ToNot(HaveOccurred())
				}
			})
		})
		Context("that are not supposed to be kept", func() {
			BeforeEach(func() {
				secrets = generateAndRegisterDeletedPodSecrets(clientBuilder)
				application = generateApplication()
			})
			It("should fail, when the secrets are mounted as SecretVolume", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretVolume(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).To(HaveOccurred())
				}
			})
			It("should fail, when the secrets are mounted as SecretValueFrom", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretValueFrom(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).To(HaveOccurred())
				}
			})
			It("should fail, when the secrets are mounted as SecretEnvFrom", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretEnvFrom(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).To(HaveOccurred())
				}
			})
		})
	})
	When("Opensearch instance with an individual secret", func() {
		Context("that are supposed to be kept", func() {
			BeforeEach(func() {
				secrets = generateAndRegisterKeptPodSecrets(clientBuilder)
				application = aiven_nais_io_v1.NewAivenApplicationBuilder(MyAppName, MyNamespace).
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						OpenSearch: &aiven_nais_io_v1.OpenSearchSpec{
							Instance:   "OpenSearchInstance",
							Access:     "read",
							SecretName: CurrentlyRequestedSecret,
						},
					}).
					Build()
				application.SetLabels(map[string]string{
					constants.AppLabel: MyAppName,
				})
			})
			It("should succeed, when the secrets are mounted as SecretVolume", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretVolume(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).ToNot(HaveOccurred())
				}
			})
			It("should succeed, when the secrets are mounted as SecretValueFrom", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretValueFrom(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).ToNot(HaveOccurred())
				}
			})
			It("should succeed, when the secrets are mounted as SecretEnvFrom", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretEnvFrom(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).ToNot(HaveOccurred())
				}
			})
		})
		Context("that are not supposed to be kept", func() {
			BeforeEach(func() {
				secrets = generateAndRegisterDeletedPodSecrets(clientBuilder)
				application = generateApplication()
			})
			It("should fail, when the secrets are mounted as SecretVolume", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretVolume(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).To(HaveOccurred())
				}
			})
			It("should fail, when the secrets are mounted as SecretValueFrom", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretValueFrom(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).To(HaveOccurred())
				}
			})
			It("should fail, when the secrets are mounted as SecretEnvFrom", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretEnvFrom(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).To(HaveOccurred())
				}
			})
		})
	})
	When("Valkey instance with an individual secret", func() {
		Context("that are supposed to be kept", func() {
			BeforeEach(func() {
				secrets = generateAndRegisterKeptPodSecrets(clientBuilder)
				application = aiven_nais_io_v1.NewAivenApplicationBuilder(MyAppName, MyNamespace).
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						Valkey: []*aiven_nais_io_v1.ValkeySpec{
							{
								Instance:   "ValkeyInstance",
								Access:     "read",
								SecretName: CurrentlyRequestedSecret,
							},
						},
					}).
					Build()
				application.SetLabels(map[string]string{
					constants.AppLabel: MyAppName,
				})
			})
			It("should succeed, when the secrets are mounted as SecretVolume", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretVolume(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).ToNot(HaveOccurred())
				}
			})
			It("should succeed, when the secrets are mounted as SecretValueFrom", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretValueFrom(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).ToNot(HaveOccurred())
				}
			})
			It("should succeed, when the secrets are mounted as SecretEnvFrom", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretEnvFrom(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).ToNot(HaveOccurred())
				}
			})
		})
		Context("that are not supposed to be kept", func() {
			BeforeEach(func() {
				secrets = generateAndRegisterDeletedPodSecrets(clientBuilder)
				application = generateApplication()
			})
			It("should fail, when the secrets are mounted as SecretVolume", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretVolume(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).To(HaveOccurred())
				}
			})
			It("should fail, when the secrets are mounted as SecretValueFrom", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretValueFrom(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).To(HaveOccurred())
				}
			})
			It("should fail, when the secrets are mounted as SecretEnvFrom", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretEnvFrom(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).To(HaveOccurred())
				}
			})
		})
	})
	When("Kafka instance with an individual secret", func() {
		Context("that are supposed to be kept", func() {
			BeforeEach(func() {
				secrets = generateAndRegisterKeptPodSecrets(clientBuilder)
				application = aiven_nais_io_v1.NewAivenApplicationBuilder(MyAppName, MyNamespace).
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
						Kafka: &aiven_nais_io_v1.KafkaSpec{
							Pool:       "KafkaPool",
							SecretName: CurrentlyRequestedSecret,
						},
					}).
					Build()
				application.SetLabels(map[string]string{
					constants.AppLabel: MyAppName,
				})
			})
			It("should succeed, when the secrets are mounted as SecretVolume", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretVolume(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).ToNot(HaveOccurred())
				}
			})
			It("should succeed, when the secrets are mounted as SecretValueFrom", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretValueFrom(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).ToNot(HaveOccurred())
				}
			})
			It("should succeed, when the secrets are mounted as SecretEnvFrom", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretEnvFrom(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).ToNot(HaveOccurred())
				}
			})
		})
		Context("that are not supposed to be kept", func() {
			BeforeEach(func() {
				secrets = generateAndRegisterDeletedPodSecrets(clientBuilder)
				application = generateApplication()
			})
			It("should fail, when the secrets are mounted as SecretVolume", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretVolume(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).To(HaveOccurred())
				}
			})
			It("should fail, when the secrets are mounted as SecretValueFrom", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretValueFrom(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).To(HaveOccurred())
				}
			})
			It("should fail, when the secrets are mounted as SecretEnvFrom", func() {
				clientBuilder.WithRuntimeObjects(
					makePodForSecretEnvFrom(SecretUsedByPod),
					&application,
				)
				janitor := buildJanitor(clientBuilder.Build(), logger)
				err := janitor.CleanUnusedSecretsForApplication(ctx, application)
				Expect(err).ToNot(HaveOccurred())

				for _, tt := range secrets {
					By(tt.reason)
					actual := &corev1.Secret{}
					err = janitor.Client.Get(context.Background(), k8sClient.ObjectKey{
						Namespace: tt.namespace,
						Name:      tt.name,
					}, actual)
					Expect(err).To(HaveOccurred())
				}
			})
		})
	})
	It("returns error when listing secrets fails", func() {
		interactions := []interaction{
			{
				method:     "List",
				arguments:  []any{mock.Anything, mock.AnythingOfType("*v1.SecretList"), mock.AnythingOfType("client.MatchingLabels"), mock.AnythingOfType("client.InNamespace")},
				returnArgs: []any{fmt.Errorf("api error")},
			},
		}

		janitor, mockClient := newJanitorWithInteractions(interactions)
		app := aiven_nais_io_v1.NewAivenApplicationBuilder("", "").Build()

		err := janitor.CleanUnusedSecretsForApplication(ctx, app)
		Expect(err).To(MatchError("failed to retrieve list of secrets: api error"))
		mockClient.AssertExpectations(GinkgoT())
	})

	It("returns error when listing pods fails", func() {
		interactions := []interaction{
			{
				method:     "List",
				arguments:  []any{mock.Anything, mock.AnythingOfType("*v1.SecretList"), mock.AnythingOfType("client.MatchingLabels"), mock.AnythingOfType("client.InNamespace")},
				returnArgs: []any{nil},
			},
			{
				method:     "List",
				arguments:  []any{mock.Anything, mock.AnythingOfType("*v1.PodList")},
				returnArgs: []any{fmt.Errorf("api error")},
			},
			{
				method:     "List",
				arguments:  []any{mock.Anything, mock.AnythingOfType("*aiven_nais_io_v1.AivenApplicationList"), mock.AnythingOfType("client.MatchingLabels")},
				returnArgs: []any{nil},
			},
			{
				method:     "List",
				arguments:  []any{mock.Anything, mock.AnythingOfType("*v1.ReplicaSetList"), mock.AnythingOfType("client.MatchingLabels")},
				returnArgs: []any{nil},
			},
			{
				method:     "List",
				arguments:  []any{mock.Anything, mock.AnythingOfType("*v1.CronJobList"), mock.AnythingOfType("client.MatchingLabels")},
				returnArgs: []any{nil},
			},
			{
				method:     "List",
				arguments:  []any{mock.Anything, mock.AnythingOfType("*v1.JobList"), mock.AnythingOfType("client.MatchingLabels")},
				returnArgs: []any{nil},
			},
		}

		janitor, mockClient := newJanitorWithInteractions(interactions)
		app := aiven_nais_io_v1.NewAivenApplicationBuilder("", "").Build()

		err := janitor.CleanUnusedSecretsForApplication(ctx, app)
		Expect(err).To(MatchError("failed to retrieve list of pods: api error"))
		mockClient.AssertExpectations(GinkgoT())
	})

	It("ignores NotFound when deleting a secret", func() {
		interactions := []interaction{
			{
				method:     "List",
				arguments:  []any{mock.Anything, mock.AnythingOfType("*v1.SecretList"), mock.AnythingOfType("client.MatchingLabels"), mock.AnythingOfType("client.InNamespace")},
				returnArgs: []any{nil},
				runFunc: func(arguments mock.Arguments) {
					if secretList, ok := arguments.Get(1).(*corev1.SecretList); ok {
						secretList.Items = []corev1.Secret{*makeSecret(UnusedSecret, MyNamespace, constants.AivenatorSecretType, MyAppName)}
					}
				},
			},
			{
				method:     "List",
				arguments:  []any{mock.Anything, mock.AnythingOfType("*v1.PodList")},
				returnArgs: []any{nil},
			},
			{
				method:     "List",
				arguments:  []any{mock.Anything, mock.AnythingOfType("*aiven_nais_io_v1.AivenApplicationList"), mock.AnythingOfType("client.MatchingLabels")},
				returnArgs: []any{nil},
			},
			{
				method:     "List",
				arguments:  []any{mock.Anything, mock.AnythingOfType("*v1.ReplicaSetList"), mock.AnythingOfType("client.MatchingLabels")},
				returnArgs: []any{nil},
			},
			{
				method:     "List",
				arguments:  []any{mock.Anything, mock.AnythingOfType("*v1.CronJobList"), mock.AnythingOfType("client.MatchingLabels")},
				returnArgs: []any{nil},
			},
			{
				method:     "List",
				arguments:  []any{mock.Anything, mock.AnythingOfType("*v1.JobList"), mock.AnythingOfType("client.MatchingLabels")},
				returnArgs: []any{nil},
			},
			{
				method:     "Delete",
				arguments:  []any{mock.Anything, mock.AnythingOfType("*v1.Secret")},
				returnArgs: []any{errors.NewNotFound(corev1.Resource("secret"), UnusedSecret)},
			},
		}

		janitor, mockClient := newJanitorWithInteractions(interactions)
		app := aiven_nais_io_v1.NewAivenApplicationBuilder("", "").Build()

		err := janitor.CleanUnusedSecretsForApplication(ctx, app)
		Expect(err).To(Succeed())
		mockClient.AssertExpectations(GinkgoT())
	})

	It("continues after an error deleting a secret", func() {
		interactions := []interaction{
			{
				method:     "List",
				arguments:  []any{mock.Anything, mock.AnythingOfType("*v1.SecretList"), mock.AnythingOfType("client.MatchingLabels"), mock.AnythingOfType("client.InNamespace")},
				returnArgs: []any{nil},
				runFunc: func(arguments mock.Arguments) {
					if secretList, ok := arguments.Get(1).(*corev1.SecretList); ok {
						secretList.Items = []corev1.Secret{
							*makeSecret(UnusedSecret, MyNamespace, constants.AivenatorSecretType, MyAppName),
							*makeSecret(NotOurSecretTypeSecret, MyNamespace, constants.AivenatorSecretType, MyAppName),
						}
					}
				},
			},
			{
				method:     "List",
				arguments:  []any{mock.Anything, mock.AnythingOfType("*v1.PodList")},
				returnArgs: []any{nil},
			},
			{
				method:     "List",
				arguments:  []any{mock.Anything, mock.AnythingOfType("*aiven_nais_io_v1.AivenApplicationList"), mock.AnythingOfType("client.MatchingLabels")},
				returnArgs: []any{nil},
			},
			{
				method:     "List",
				arguments:  []any{mock.Anything, mock.AnythingOfType("*v1.ReplicaSetList"), mock.AnythingOfType("client.MatchingLabels")},
				returnArgs: []any{nil},
			},
			{
				method:     "List",
				arguments:  []any{mock.Anything, mock.AnythingOfType("*v1.CronJobList"), mock.AnythingOfType("client.MatchingLabels")},
				returnArgs: []any{nil},
			},
			{
				method:     "List",
				arguments:  []any{mock.Anything, mock.AnythingOfType("*v1.JobList"), mock.AnythingOfType("client.MatchingLabels")},
				returnArgs: []any{nil},
			},
			{
				method: "Delete",
				arguments: []any{mock.Anything, mock.MatchedBy(func(s *corev1.Secret) bool {
					return s.GetName() == UnusedSecret
				})},
				returnArgs: []any{fmt.Errorf("api error")},
			},
			{
				method: "Delete",
				arguments: []any{mock.Anything, mock.MatchedBy(func(s *corev1.Secret) bool {
					return s.GetName() == NotOurSecretTypeSecret
				})},
				returnArgs: []any{nil},
			},
		}

		janitor, mockClient := newJanitorWithInteractions(interactions)
		app := aiven_nais_io_v1.NewAivenApplicationBuilder("", "").Build()

		err := janitor.CleanUnusedSecretsForApplication(ctx, app)
		Expect(err).To(Succeed())
		mockClient.AssertExpectations(GinkgoT())
	})
})

func makePodForSecretVolume(secretName string) *corev1.Pod {
	return &corev1.Pod{
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: secretName,
						},
					},
				},
			},
		},
	}
}

func makePodForSecretValueFrom(secretName string) *corev1.Pod {
	return &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "container",
					Env: []corev1.EnvVar{
						{
							Name: "AIVEN_SECRET",
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: secretName,
									},
									Key: "my-secret",
								},
							},
						},
					},
				},
			},
		},
	}
}

func makePodForSecretEnvFrom(secretName string) *corev1.Pod {
	return &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "container",
					EnvFrom: []corev1.EnvFromSource{
						{
							SecretRef: &corev1.SecretEnvSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: secretName,
								},
							},
						},
					},
				},
			},
		},
	}
}

type makeSecretOpts struct {
	protected        bool
	hasNoAnnotations bool
	hasTimeLimit     bool
	expiresAt        *time.Time
}

type MakeSecretOption func(opts *makeSecretOpts)

func SecretHasNoAnnotations(opts *makeSecretOpts) {
	opts.hasNoAnnotations = true
}

func SecretIsProtected(opts *makeSecretOpts) {
	opts.protected = true
}

func SecretHasTimeLimit(opts *makeSecretOpts) {
	opts.hasTimeLimit = true
}

func SecretExpiresAt(expiresAt time.Time) func(opts *makeSecretOpts) {
	return func(opts *makeSecretOpts) {
		opts.expiresAt = &expiresAt
	}
}

func makeSecret(name, namespace, secretType, appName string, optFuncs ...MakeSecretOption) *corev1.Secret {
	opts := &makeSecretOpts{}
	for _, optFunc := range optFuncs {
		optFunc(opts)
	}
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				constants.AppLabel:        appName,
				constants.TeamLabel:       namespace,
				constants.SecretTypeLabel: secretType,
			},
		},
	}
	if !opts.hasNoAnnotations || opts.protected {
		s.SetAnnotations(map[string]string{
			constants.AivenatorProtectedKey: strconv.FormatBool(opts.protected),
		})
		s.ObjectMeta.Labels[constants.AivenatorProtectedKey] = strconv.FormatBool(opts.protected)
	}

	if opts.hasTimeLimit {
		annotations := s.GetAnnotations()
		s.SetAnnotations(utils.MergeStringMap(annotations, map[string]string{
			constants.AivenatorProtectedWithTimeLimitAnnotation: strconv.FormatBool(opts.hasTimeLimit),
		}))
	}

	if opts.expiresAt != nil {
		annotations := s.GetAnnotations()
		s.SetAnnotations(utils.MergeStringMap(annotations, map[string]string{
			constants.AivenatorProtectedExpiresAtAnnotation: opts.expiresAt.Format(time.RFC3339),
		}))
	}
	return s
}
