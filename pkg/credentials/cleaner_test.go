package credentials

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/nais/liberator/pkg/scheme"
	"k8s.io/apimachinery/pkg/runtime"

	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/utils"
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

type JanitorTestSuite struct {
	suite.Suite

	logger        *log.Entry
	ctx           context.Context
	clientBuilder *fake.ClientBuilder
}

func (suite *JanitorTestSuite) SetupSuite() {
	suite.logger = log.NewEntry(log.New())
	suite.ctx = context.Background()
}

func (suite *JanitorTestSuite) SetupTest() {
	suite.clientBuilder = fake.NewClientBuilder()
	s := runtime.NewScheme()
	_, err := scheme.AddAll(s)
	if err != nil {
		suite.FailNowf("failed setup", "error adding runtime types to scheme: %v", err)
	}
	suite.clientBuilder.WithScheme(s)
}

func (suite *JanitorTestSuite) buildJanitor(client Client) *Cleaner {
	return &Cleaner{
		Client: client,
		Logger: suite.logger,
	}
}

func (suite *JanitorTestSuite) TestNoSecretsFound() {
	suite.clientBuilder.WithRuntimeObjects(
		makeSecret(UnusedSecret, NotMyNamespace, constants.AivenatorSecretType, NotMyAppName),
		makeSecret(NotOurSecretTypeSecret, NotMyNamespace, constants.AivenatorSecretType, MyAppName),
	)
	client := suite.clientBuilder.Build()
	janitor := suite.buildJanitor(client)
	application := aiven_nais_io_v1.NewAivenApplicationBuilder(MyAppName, MyNamespace).Build()
	errs := janitor.CleanUnusedSecretsForApplication(suite.ctx, application)

	suite.Empty(errs)
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

func generateAndRegisterPodSecrets(suite *JanitorTestSuite) []secretSetup {
	secrets := []secretSetup{
		{UnusedSecret, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{}, false, "Unused secret should be deleted"},
		{UnusedSecret, NotMyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{}, true, "Secret in another namespace should be kept"},
		{NotOurSecretTypeSecret, MyNamespace, NotMySecretType, MyAppName, []MakeSecretOption{}, true, "Unrelated secret should be kept"},
		{SecretUsedByPod, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{}, true, "Used secret should be kept"},
		{ProtectedNotTimeLimited, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{SecretIsProtected}, true, "Protected secret should be kept"},
		{UnusedSecretWithNoAnnotations, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{SecretHasNoAnnotations}, false, "Unused secret should be deleted, even if annotations are nil"},
		{SecretBelongingToOtherApp, MyNamespace, constants.AivenatorSecretType, NotMyAppName, []MakeSecretOption{}, true, "Secret belonging to different app should be kept"},
		{CurrentlyRequestedSecret, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{}, true, "Secret currently requested should be kept"},
		{ProtectedNotExpired, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{SecretIsProtected, SecretHasTimeLimit, SecretExpiresAt(time.Now().Add(48 * time.Hour))}, true, "Protected secret with time-limit that isn't expired should be kept"},
		{ProtectedExpired, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{SecretIsProtected, SecretHasTimeLimit, SecretExpiresAt(time.Now().Add(-48 * time.Hour))}, false, "Protected secret with time-limit that is expired should be deleted"},
		{ProtectedTimeLimitedWithNoExpirySet, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{SecretIsProtected, SecretHasTimeLimit}, true, "Protected secret with time-limit but missing expires date should be kept"},
	}
	for _, s := range secrets {
		suite.clientBuilder.WithRuntimeObjects(makeSecret(s.name, s.namespace, s.secretType, s.appName, s.opts...))
	}
	return secrets
}

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

func (suite *JanitorTestSuite) TestUnusedVolumeMountedSecretsFound() {
	secrets := generateAndRegisterPodSecrets(suite)
	application := generateApplication()

	suite.clientBuilder.WithRuntimeObjects(
		makePodForSecretVolume(SecretUsedByPod),
		&application,
	)

	// Unique per of these tests
	janitor := suite.buildJanitor(suite.clientBuilder.Build())
	err := janitor.CleanUnusedSecretsForApplication(suite.ctx, application)
	suite.Nil(err)

	for _, tt := range secrets {
		suite.Run(tt.reason, func() {
			actual := &corev1.Secret{}
			err := janitor.Client.Get(context.Background(), client.ObjectKey{
				Namespace: tt.namespace,
				Name:      tt.name,
			}, actual)
			suite.NotEqualf(tt.wanted, errors.IsNotFound(err), tt.reason)
		})
	}
}

func (suite *JanitorTestSuite) TestUnusedEnvMountedSecretsFound() {
	secrets := generateAndRegisterPodSecrets(suite)
	application := generateApplication()

	// Unique per of these tests
	suite.clientBuilder.WithRuntimeObjects(
		makePodForSecretValueFrom(SecretUsedByPod),
		&application,
	)

	janitor := suite.buildJanitor(suite.clientBuilder.Build())
	err := janitor.CleanUnusedSecretsForApplication(suite.ctx, application)
	suite.Nil(err)

	for _, tt := range secrets {
		suite.Run(tt.reason, func() {
			actual := &corev1.Secret{}
			err := janitor.Client.Get(context.Background(), client.ObjectKey{
				Namespace: tt.namespace,
				Name:      tt.name,
			}, actual)
			suite.NotEqualf(tt.wanted, errors.IsNotFound(err), tt.reason)
		})
	}
}

func (suite *JanitorTestSuite) TestUnusedEnvFromMountedSecretsFound() {
	secrets := generateAndRegisterPodSecrets(suite)
	application := generateApplication()

	// Unique per of these tests
	suite.clientBuilder.WithRuntimeObjects(
		makePodForSecretEnvFrom(SecretUsedByPod),
		&application,
	)
	janitor := suite.buildJanitor(suite.clientBuilder.Build())
	err := janitor.CleanUnusedSecretsForApplication(suite.ctx, application)
	suite.Nil(err)

	for _, tt := range secrets {
		suite.Run(tt.reason, func() {
			actual := &corev1.Secret{}
			err := janitor.Client.Get(context.Background(), client.ObjectKey{
				Namespace: tt.namespace,
				Name:      tt.name,
			}, actual)
			suite.NotEqualf(tt.wanted, errors.IsNotFound(err), tt.reason)
		})
	}
}

func (suite *JanitorTestSuite) TestOpenSearchIndividualSecret() {
	secrets := generateAndRegisterPodSecrets(suite)
	application := aiven_nais_io_v1.NewAivenApplicationBuilder(MyAppName, MyNamespace).
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

	suite.clientBuilder.WithRuntimeObjects(
		makePodForSecretValueFrom(SecretUsedByPod),
		&application,
	)

	janitor := suite.buildJanitor(suite.clientBuilder.Build())
	err := janitor.CleanUnusedSecretsForApplication(suite.ctx, application)
	suite.Nil(err)

	for _, tt := range secrets {
		suite.Run(tt.reason, func() {
			actual := &corev1.Secret{}
			err := janitor.Client.Get(context.Background(), client.ObjectKey{
				Namespace: tt.namespace,
				Name:      tt.name,
			}, actual)
			suite.NotEqualf(tt.wanted, errors.IsNotFound(err), tt.reason)
		})
	}
}

func (suite *JanitorTestSuite) TestValkeyIndividualSecret() {
	secrets := generateAndRegisterPodSecrets(suite)
	application := aiven_nais_io_v1.NewAivenApplicationBuilder(MyAppName, MyNamespace).
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

	suite.clientBuilder.WithRuntimeObjects(
		makePodForSecretValueFrom(SecretUsedByPod),
		&application,
	)

	janitor := suite.buildJanitor(suite.clientBuilder.Build())
	err := janitor.CleanUnusedSecretsForApplication(suite.ctx, application)
	suite.Nil(err)

	for _, tt := range secrets {
		suite.Run(tt.reason, func() {
			actual := &corev1.Secret{}
			err := janitor.Client.Get(context.Background(), client.ObjectKey{
				Namespace: tt.namespace,
				Name:      tt.name,
			}, actual)
			suite.NotEqualf(tt.wanted, errors.IsNotFound(err), tt.reason)
		})
	}
}

func (suite *JanitorTestSuite) TestKafkaIndividualSecret() {
	secrets := generateAndRegisterPodSecrets(suite)
	application := aiven_nais_io_v1.NewAivenApplicationBuilder(MyAppName, MyNamespace).
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

	suite.clientBuilder.WithRuntimeObjects(
		makePodForSecretValueFrom(SecretUsedByPod),
		&application,
	)

	janitor := suite.buildJanitor(suite.clientBuilder.Build())
	err := janitor.CleanUnusedSecretsForApplication(suite.ctx, application)
	suite.Nil(err)

	for _, tt := range secrets {
		suite.Run(tt.reason, func() {
			actual := &corev1.Secret{}
			err := janitor.Client.Get(context.Background(), client.ObjectKey{
				Namespace: tt.namespace,
				Name:      tt.name,
			}, actual)
			suite.NotEqualf(tt.wanted, errors.IsNotFound(err), tt.reason)
		})
	}
}

func (suite *JanitorTestSuite) TestErrors() {
	type interaction struct {
		method     string
		arguments  []any
		returnArgs []any
		runFunc    func(arguments mock.Arguments)
	}
	tests := []struct {
		name         string
		interactions []interaction
		expected     error
	}{
		{
			name: "TestErrorGettingSecrets",
			interactions: []interaction{
				{
					"List",
					[]any{mock.Anything, mock.AnythingOfType("*v1.SecretList"), mock.AnythingOfType("client.MatchingLabels"), mock.AnythingOfType("client.InNamespace")},
					[]any{fmt.Errorf("api error")},
					nil,
				},
			},
			expected: fmt.Errorf("failed to retrieve list of secrets: api error"),
		},
		{
			name: "TestErrorGettingPods",
			interactions: []interaction{
				{
					"List",
					[]any{mock.Anything, mock.AnythingOfType("*v1.SecretList"), mock.AnythingOfType("client.MatchingLabels"), mock.AnythingOfType("client.InNamespace")},
					[]any{nil},
					nil,
				},
				{
					"List",
					[]any{mock.Anything, mock.AnythingOfType("*v1.PodList")},
					[]any{fmt.Errorf("api error")},
					nil,
				},
				{
					"List",
					[]any{mock.Anything, mock.AnythingOfType("*aiven_nais_io_v1.AivenApplicationList"), mock.AnythingOfType("client.MatchingLabels")},
					[]any{nil},
					nil,
				},
				{
					"List",
					[]any{mock.Anything, mock.AnythingOfType("*v1.ReplicaSetList"), mock.AnythingOfType("client.MatchingLabels")},
					[]any{nil},
					nil,
				},
				{
					"List",
					[]any{mock.Anything, mock.AnythingOfType("*v1.CronJobList"), mock.AnythingOfType("client.MatchingLabels")},
					[]any{nil},
					nil,
				},
				{
					"List",
					[]any{mock.Anything, mock.AnythingOfType("*v1.JobList"), mock.AnythingOfType("client.MatchingLabels")},
					[]any{nil},
					nil,
				},
			},
			expected: fmt.Errorf("failed to retrieve list of pods: api error"),
		},
		{
			name: "TestSecretNotFoundWhenDeleting",
			interactions: []interaction{
				{
					"List",
					[]any{mock.Anything, mock.AnythingOfType("*v1.SecretList"), mock.AnythingOfType("client.MatchingLabels"), mock.AnythingOfType("client.InNamespace")},
					[]any{nil},
					func(arguments mock.Arguments) {
						if secretList, ok := arguments.Get(1).(*corev1.SecretList); ok {
							secretList.Items = []corev1.Secret{*makeSecret(UnusedSecret, MyNamespace, constants.AivenatorSecretType, MyAppName)}
						}
					},
				},
				{
					"List",
					[]any{mock.Anything, mock.AnythingOfType("*v1.PodList")},
					[]any{nil},
					nil,
				},
				{
					"List",
					[]any{mock.Anything, mock.AnythingOfType("*aiven_nais_io_v1.AivenApplicationList"), mock.AnythingOfType("client.MatchingLabels")},
					[]any{nil},
					nil,
				},
				{
					"List",
					[]any{mock.Anything, mock.AnythingOfType("*v1.ReplicaSetList"), mock.AnythingOfType("client.MatchingLabels")},
					[]any{nil},
					nil,
				},
				{
					"List",
					[]any{mock.Anything, mock.AnythingOfType("*v1.CronJobList"), mock.AnythingOfType("client.MatchingLabels")},
					[]any{nil},
					nil,
				},
				{
					"List",
					[]any{mock.Anything, mock.AnythingOfType("*v1.JobList"), mock.AnythingOfType("client.MatchingLabels")},
					[]any{nil},
					nil,
				},
				{
					"Delete",
					[]any{mock.Anything, mock.AnythingOfType("*v1.Secret")},
					[]any{errors.NewNotFound(corev1.Resource("secret"), UnusedSecret)},
					nil,
				},
			},
			expected: nil,
		},
		{
			name: "TestContinueAfterErrorDeletingSecret",
			interactions: []interaction{
				{
					"List",
					[]any{mock.Anything, mock.AnythingOfType("*v1.SecretList"), mock.AnythingOfType("client.MatchingLabels"), mock.AnythingOfType("client.InNamespace")},
					[]any{nil},
					func(arguments mock.Arguments) {
						if secretList, ok := arguments.Get(1).(*corev1.SecretList); ok {
							secretList.Items = []corev1.Secret{
								*makeSecret(UnusedSecret, MyNamespace, constants.AivenatorSecretType, MyAppName),
								*makeSecret(NotOurSecretTypeSecret, MyNamespace, constants.AivenatorSecretType, MyAppName),
							}
						}
					},
				},
				{
					"List",
					[]any{mock.Anything, mock.AnythingOfType("*v1.PodList")},
					[]any{nil},
					nil,
				},
				{
					"List",
					[]any{mock.Anything, mock.AnythingOfType("*aiven_nais_io_v1.AivenApplicationList"), mock.AnythingOfType("client.MatchingLabels")},
					[]any{nil},
					nil,
				},
				{
					"List",
					[]any{mock.Anything, mock.AnythingOfType("*v1.ReplicaSetList"), mock.AnythingOfType("client.MatchingLabels")},
					[]any{nil},
					nil,
				},
				{
					"List",
					[]any{mock.Anything, mock.AnythingOfType("*v1.CronJobList"), mock.AnythingOfType("client.MatchingLabels")},
					[]any{nil},
					nil,
				},
				{
					"List",
					[]any{mock.Anything, mock.AnythingOfType("*v1.JobList"), mock.AnythingOfType("client.MatchingLabels")},
					[]any{nil},
					nil,
				},
				{
					"Delete",
					[]any{mock.Anything, mock.MatchedBy(func(s *corev1.Secret) bool {
						return s.GetName() == UnusedSecret
					})},
					[]any{fmt.Errorf("api error")},
					nil,
				},
				{
					"Delete",
					[]any{mock.Anything, mock.MatchedBy(func(s *corev1.Secret) bool {
						return s.GetName() == NotOurSecretTypeSecret
					})},
					[]any{nil},
					nil,
				},
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		suite.Run(tt.name, func() {
			mockClient := &MockClient{}
			for _, i := range tt.interactions {
				call := mockClient.On(i.method, i.arguments...).Return(i.returnArgs...)
				if i.runFunc != nil {
					call.Run(i.runFunc)
				}
			}
			mockClient.On("Scheme").Maybe().Return(&runtime.Scheme{})

			janitor := suite.buildJanitor(mockClient)
			application := aiven_nais_io_v1.NewAivenApplicationBuilder("", "").Build()
			err := janitor.CleanUnusedSecretsForApplication(suite.ctx, application)

			suite.Equal(tt.expected, err)
			mockClient.AssertExpectations(suite.T())
		})
	}
}

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

func TestJanitor(t *testing.T) {
	janitorTestSuite := new(JanitorTestSuite)
	suite.Run(t, janitorTestSuite)
}
