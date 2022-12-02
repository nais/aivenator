package credentials

import (
	"context"
	"fmt"
	"github.com/nais/aivenator/pkg/mocks"
	"k8s.io/apimachinery/pkg/runtime"
	"strconv"
	"testing"
	"time"

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

	Secret1Name  = "secret1"
	Secret2Name  = "secret2"
	Secret3Name  = "secret3"
	Secret4Name  = "secret4"
	Secret5Name  = "secret5"
	Secret6Name  = "secret6"
	Secret7Name  = "secret7"
	Secret8Name  = "secret8"
	Secret9Name  = "secret9"
	Secret10Name = "secret10"

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
}

func (suite *JanitorTestSuite) buildJanitor(client Client) *Janitor {
	return &Janitor{
		Client: client,
		Logger: suite.logger,
	}
}

func (suite *JanitorTestSuite) TestNoSecretsFound() {
	suite.clientBuilder.WithRuntimeObjects(
		makeSecret(Secret1Name, NotMyNamespace, constants.AivenatorSecretType, NotMyAppName),
		makeSecret(Secret2Name, NotMyNamespace, constants.AivenatorSecretType, MyAppName),
	)
	client := suite.clientBuilder.Build()
	janitor := suite.buildJanitor(client)
	application := aiven_nais_io_v1.NewAivenApplicationBuilder(MyAppName, MyNamespace).Build()
	errs := janitor.CleanUnusedSecrets(suite.ctx, application)

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

func (suite *JanitorTestSuite) TestUnusedSecretsFound() {
	secrets := []secretSetup{
		{Secret1Name, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{}, false, "Unused secret should be deleted"},
		{Secret1Name, NotMyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{}, true, "Secret in another namespace should be kept"},
		{Secret2Name, MyNamespace, NotMySecretType, MyAppName, []MakeSecretOption{}, true, "Unrelated secret should be kept"},
		{Secret3Name, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{}, true, "Used secret should be kept"},
		{Secret4Name, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{SecretIsProtected}, true, "Protected secret should be kept"},
		{Secret5Name, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{SecretHasNoAnnotations}, false, "Unused secret should be deleted, even if annotations are nil"},
		{Secret6Name, MyNamespace, constants.AivenatorSecretType, NotMyAppName, []MakeSecretOption{}, true, "Secret belonging to different app should be kept"},
		{Secret7Name, MyNamespace, constants.AivenatorSecretType, MyAppName, []MakeSecretOption{}, true, "Secret currently requested should be kept"},
	}
	for _, s := range secrets {
		suite.clientBuilder.WithRuntimeObjects(makeSecret(s.name, s.namespace, s.secretType, s.appName, s.opts...))
	}
	suite.clientBuilder.WithRuntimeObjects(
		makePodForSecret(Secret3Name),
	)

	janitor := suite.buildJanitor(suite.clientBuilder.Build())
	application := aiven_nais_io_v1.NewAivenApplicationBuilder(MyAppName, MyNamespace).
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			SecretName: Secret7Name,
		}).
		Build()
	errs := janitor.CleanUnusedSecrets(suite.ctx, application)

	suite.Empty(errs)

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

func (suite *JanitorTestSuite) TestUnusedSecretsFoundWithProtectionAndNotExpired() {
	secrets := []secretSetup{
		{Secret8Name, MyNamespace, constants.AivenatorSecretType, MyUser, []MakeSecretOption{SecretIsProtected, SecretIsExpired}, true, "Protected and time limited secret should be kept if not expired"},
	}
	for _, s := range secrets {
		suite.clientBuilder.WithRuntimeObjects(makeSecret(s.name, s.namespace, s.secretType, s.appName, s.opts...))
	}

	janitor := suite.buildJanitor(suite.clientBuilder.Build())
	application8 := aiven_nais_io_v1.NewAivenApplicationBuilder(MyUser, MyNamespace).
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			SecretName: "some-other-secret-8",
			ExpiresAt:  &metav1.Time{Time: time.Now().AddDate(0, 0, 2)},
		}).
		Build()
	errs := janitor.CleanUnusedSecrets(suite.ctx, application8)

	suite.Empty(errs)

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

func (suite *JanitorTestSuite) TestUnusedSecretsFoundWithProtectionAndExpired() {
	secrets := []secretSetup{
		{Secret9Name, MyNamespace, constants.AivenatorSecretType, MyUser, []MakeSecretOption{SecretIsProtected, SecretIsExpired}, false, "Protected and time limited secret should be deleted if expired"},
	}
	for _, s := range secrets {
		suite.clientBuilder.WithRuntimeObjects(makeSecret(s.name, s.namespace, s.secretType, s.appName, s.opts...))
	}

	janitor := suite.buildJanitor(suite.clientBuilder.Build())

	application9 := aiven_nais_io_v1.NewAivenApplicationBuilder(MyUser, MyNamespace).
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			SecretName: "some-other-secret-9",
			ExpiresAt:  &metav1.Time{Time: time.Now().AddDate(0, 0, -2)},
		}).
		Build()

	errs := janitor.CleanUnusedSecrets(suite.ctx, application9)
	suite.Empty(errs)

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

func (suite *JanitorTestSuite) TestUnusedSecretsFoundWithProtectionAndExpiredAtNotSet() {
	secrets := []secretSetup{
		{Secret10Name, MyNamespace, constants.AivenatorSecretType, MyUser, []MakeSecretOption{SecretIsProtected, SecretIsExpired}, true, "Protected and time limited secret should be left alone if Spec.ExpiresAt is not set"},
	}
	for _, s := range secrets {
		suite.clientBuilder.WithRuntimeObjects(makeSecret(s.name, s.namespace, s.secretType, s.appName, s.opts...))
	}

	janitor := suite.buildJanitor(suite.clientBuilder.Build())

	application10 := aiven_nais_io_v1.NewAivenApplicationBuilder(MyUser, MyNamespace).
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			SecretName: "some-other-secret-10",
		}).
		Build()

	errs := janitor.CleanUnusedSecrets(suite.ctx, application10)
	suite.Empty(errs)

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
		arguments  []interface{}
		returnArgs []interface{}
		runFunc    func(arguments mock.Arguments)
	}
	tests := []struct {
		name         string
		interactions []interaction
		expected     []error
	}{
		{
			name: "TestErrorGettingSecrets",
			interactions: []interaction{
				{
					"List",
					[]interface{}{mock.Anything, mock.AnythingOfType("*v1.SecretList"), mock.AnythingOfType("client.MatchingLabels"), mock.AnythingOfType("client.InNamespace")},
					[]interface{}{fmt.Errorf("api error")},
					nil,
				},
			},
			expected: []error{fmt.Errorf("failed to retrieve list of secrets: api error")},
		},
		{
			name: "TestErrorGettingPods",
			interactions: []interaction{
				{
					"List",
					[]interface{}{mock.Anything, mock.AnythingOfType("*v1.SecretList"), mock.AnythingOfType("client.MatchingLabels"), mock.AnythingOfType("client.InNamespace")},
					[]interface{}{nil},
					nil,
				},
				{
					"List",
					[]interface{}{mock.Anything, mock.AnythingOfType("*v1.PodList")},
					[]interface{}{fmt.Errorf("api error")},
					nil,
				},
			},
			expected: []error{fmt.Errorf("failed to retrieve list of pods: api error")},
		},
		{
			name: "TestErrorDeletingSecret",
			interactions: []interaction{
				{
					"List",
					[]interface{}{mock.Anything, mock.AnythingOfType("*v1.SecretList"), mock.AnythingOfType("client.MatchingLabels"), mock.AnythingOfType("client.InNamespace")},
					[]interface{}{nil},
					func(arguments mock.Arguments) {
						if secretList, ok := arguments.Get(1).(*corev1.SecretList); ok {
							secretList.Items = []corev1.Secret{*makeSecret(Secret1Name, MyNamespace, constants.AivenatorSecretType, MyAppName)}
						}
					},
				},
				{
					"List",
					[]interface{}{mock.Anything, mock.AnythingOfType("*v1.PodList")},
					[]interface{}{nil},
					nil,
				},
				{
					"Delete",
					[]interface{}{mock.Anything, mock.AnythingOfType("*v1.Secret")},
					[]interface{}{fmt.Errorf("api error")},
					nil,
				},
			},
			expected: []error{fmt.Errorf("failed to delete secret secret1 in namespace namespace: api error")},
		},
		{
			name: "TestSecretNotFoundWhenDeleting",
			interactions: []interaction{
				{
					"List",
					[]interface{}{mock.Anything, mock.AnythingOfType("*v1.SecretList"), mock.AnythingOfType("client.MatchingLabels"), mock.AnythingOfType("client.InNamespace")},
					[]interface{}{nil},
					func(arguments mock.Arguments) {
						if secretList, ok := arguments.Get(1).(*corev1.SecretList); ok {
							secretList.Items = []corev1.Secret{*makeSecret(Secret1Name, MyNamespace, constants.AivenatorSecretType, MyAppName)}
						}
					},
				},
				{
					"List",
					[]interface{}{mock.Anything, mock.AnythingOfType("*v1.PodList")},
					[]interface{}{nil},
					nil,
				},
				{
					"Delete",
					[]interface{}{mock.Anything, mock.AnythingOfType("*v1.Secret")},
					[]interface{}{errors.NewNotFound(corev1.Resource("secret"), Secret1Name)},
					nil,
				},
			},
			expected: []error{},
		},
		{
			name: "TestContinueAfterErrorDeletingSecret",
			interactions: []interaction{
				{
					"List",
					[]interface{}{mock.Anything, mock.AnythingOfType("*v1.SecretList"), mock.AnythingOfType("client.MatchingLabels"), mock.AnythingOfType("client.InNamespace")},
					[]interface{}{nil},
					func(arguments mock.Arguments) {
						if secretList, ok := arguments.Get(1).(*corev1.SecretList); ok {
							secretList.Items = []corev1.Secret{
								*makeSecret(Secret1Name, MyNamespace, constants.AivenatorSecretType, MyAppName),
								*makeSecret(Secret2Name, MyNamespace, constants.AivenatorSecretType, MyAppName),
							}
						}
					},
				},
				{
					"List",
					[]interface{}{mock.Anything, mock.AnythingOfType("*v1.PodList")},
					[]interface{}{nil},
					nil,
				},
				{
					"Delete",
					[]interface{}{mock.Anything, mock.MatchedBy(func(s *corev1.Secret) bool {
						return s.GetName() == Secret1Name
					})},
					[]interface{}{fmt.Errorf("api error")},
					nil,
				},
				{
					"Delete",
					[]interface{}{mock.Anything, mock.MatchedBy(func(s *corev1.Secret) bool {
						return s.GetName() == Secret2Name
					})},
					[]interface{}{nil},
					nil,
				},
			},
			expected: []error{fmt.Errorf("failed to delete secret secret1 in namespace namespace: api error")},
		},
	}

	for _, tt := range tests {
		suite.Run(tt.name, func() {
			mockClient := &mocks.Client{}
			for _, i := range tt.interactions {
				call := mockClient.On(i.method, i.arguments...).Return(i.returnArgs...)
				if i.runFunc != nil {
					call.Run(i.runFunc)
				}
			}
			mockClient.On("Scheme").Maybe().Return(&runtime.Scheme{})

			janitor := suite.buildJanitor(mockClient)
			application := aiven_nais_io_v1.NewAivenApplicationBuilder("", "").Build()
			errs := janitor.CleanUnusedSecrets(suite.ctx, application)

			suite.Equal(tt.expected, errs)
			mockClient.AssertExpectations(suite.T())
		})
	}
}

func makePodForSecret(secretName string) *corev1.Pod {
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

type makeSecretOpts struct {
	protected        bool
	hasNoAnnotations bool
	expiredAt        bool
}

type MakeSecretOption func(opts *makeSecretOpts)

func SecretHasNoAnnotations(opts *makeSecretOpts) {
	opts.hasNoAnnotations = true
}

func SecretIsProtected(opts *makeSecretOpts) {
	opts.protected = true
}

func SecretIsExpired(opts *makeSecretOpts) {
	opts.expiredAt = true
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
			constants.AivenatorProtectedAnnotation: strconv.FormatBool(opts.protected),
		})
	}

	if opts.expiredAt {
		annotations := s.GetAnnotations()
		s.SetAnnotations(utils.MergeStringMap(annotations, map[string]string{
			constants.AivenatorProtectedExpireAtAnnotation: strconv.FormatBool(opts.expiredAt),
		}))
	}
	return s
}

func TestJanitor(t *testing.T) {
	janitorTestSuite := new(JanitorTestSuite)
	suite.Run(t, janitorTestSuite)
}
