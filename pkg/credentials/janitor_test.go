package credentials

import (
	"context"
	"fmt"
	"github.com/nais/aivenator/controllers/mocks"
	"github.com/nais/aivenator/pkg/handlers/secret"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strconv"
	"testing"
)

const (
	MyAppName    = "app1"
	NotMyAppName = "app2"

	Secret1Name = "secret1"
	Secret2Name = "secret2"
	Secret3Name = "secret3"
	Secret4Name = "secret4"
	Secret5Name = "secret5"
	Secret6Name = "secret6"

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
		makeSecret(Secret1Name, NotMyNamespace, secret.AivenatorSecretType, NotMyAppName),
		makeSecret(Secret2Name, NotMyNamespace, secret.AivenatorSecretType, MyAppName),
	)
	janitor := suite.buildJanitor(suite.clientBuilder.Build())
	errs := janitor.CleanUnusedSecrets(suite.ctx, MyAppName, MyNamespace)

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
		{Secret1Name, MyNamespace, secret.AivenatorSecretType, MyAppName, []MakeSecretOption{}, false, "Unused secret should be deleted"},
		{Secret1Name, NotMyNamespace, secret.AivenatorSecretType, MyAppName, []MakeSecretOption{}, true, "Secret in another namespace should be kept"},
		{Secret2Name, MyNamespace, NotMySecretType, MyAppName, []MakeSecretOption{}, true, "Unrelated secret should be kept"},
		{Secret3Name, MyNamespace, secret.AivenatorSecretType, MyAppName, []MakeSecretOption{}, true, "Used secret should be kept"},
		{Secret4Name, MyNamespace, secret.AivenatorSecretType, MyAppName, []MakeSecretOption{SecretIsProtected}, true, "Protected secret should be kept"},
		{Secret5Name, MyNamespace, secret.AivenatorSecretType, MyAppName, []MakeSecretOption{SecretHasNoAnnotations}, false, "Unused secret should be deleted, even if annotations are nil"},
		{Secret6Name, MyNamespace, secret.AivenatorSecretType, NotMyAppName, []MakeSecretOption{}, true, "Secret belonging to different app should be kept"},
	}
	for _, s := range secrets {
		suite.clientBuilder.WithRuntimeObjects(makeSecret(s.name, s.namespace, s.secretType, s.appName, s.opts...))
	}
	suite.clientBuilder.WithRuntimeObjects(
		makePodForSecret(Secret3Name),
	)

	janitor := suite.buildJanitor(suite.clientBuilder.Build())
	errs := janitor.CleanUnusedSecrets(suite.ctx, MyAppName, MyNamespace)

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
							secretList.Items = []corev1.Secret{*makeSecret(Secret1Name, MyNamespace, secret.AivenatorSecretType, MyAppName)}
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
							secretList.Items = []corev1.Secret{*makeSecret(Secret1Name, MyNamespace, secret.AivenatorSecretType, MyAppName)}
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
								*makeSecret(Secret1Name, MyNamespace, secret.AivenatorSecretType, MyAppName),
								*makeSecret(Secret2Name, MyNamespace, secret.AivenatorSecretType, MyAppName),
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
			janitor := suite.buildJanitor(mockClient)
			errs := janitor.CleanUnusedSecrets(suite.ctx, "", "")

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
}

type MakeSecretOption func(opts *makeSecretOpts)

func SecretHasNoAnnotations(opts *makeSecretOpts) {
	opts.hasNoAnnotations = true
}

func SecretIsProtected(opts *makeSecretOpts) {
	opts.protected = true
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
				secret.AppLabel:        appName,
				secret.TeamLabel:       namespace,
				secret.SecretTypeLabel: secretType,
			},
		},
	}
	if !opts.hasNoAnnotations || opts.protected {
		s.SetAnnotations(map[string]string{
			secret.AivenatorProtectedAnnotation: strconv.FormatBool(opts.protected),
		})
	}
	return s
}

func TestJanitor(t *testing.T) {
	janitorTestSuite := new(JanitorTestSuite)
	suite.Run(t, janitorTestSuite)
}
