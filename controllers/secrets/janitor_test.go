package secrets

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
	Secret1Name = "secret1"
	Secret2Name = "secret2"
	Secret3Name = "secret3"
	Secret4Name = "secret4"
	Secret5Name = "secret5"

	Namespace = "namespace"
)

type JanitorTestSuite struct {
	suite.Suite

	logger        *log.Entry
	clientBuilder *fake.ClientBuilder
}

func (suite *JanitorTestSuite) SetupSuite() {
	suite.logger = log.NewEntry(log.New())
}

func (suite *JanitorTestSuite) SetupTest() {
	suite.clientBuilder = fake.NewClientBuilder()
}

func (suite *JanitorTestSuite) buildJanitor(client Client) *Janitor {
	return &Janitor{
		Client: client,
		Logger: suite.logger,
		Ctx:    context.Background(),
	}
}

func (suite *JanitorTestSuite) TestNoSecretsFound() {
	janitor := suite.buildJanitor(suite.clientBuilder.Build())
	err := janitor.CleanUnusedSecrets()

	suite.NoError(err)
}

func (suite *JanitorTestSuite) TestUnusedSecretsFound() {
	suite.clientBuilder.WithRuntimeObjects(
		makeSecret(Secret1Name, secret.AivenatorSecretType),
		makeSecret(Secret2Name, "other.nais.io"),
		makeSecret(Secret3Name, secret.AivenatorSecretType),
		makeSecret(Secret4Name, secret.AivenatorSecretType, SecretIsProtected),
		makeSecret(Secret5Name, secret.AivenatorSecretType, SecretHasNoAnnotations),
		makePodForSecret(Secret3Name),
	)

	expected := []struct {
		name   string
		wanted bool
		reason string
	}{
		{Secret1Name, false, "Unused secret should be deleted"},
		{Secret2Name, true, "Unrelated secret should be kept"},
		{Secret3Name, true, "Used secret should be kept"},
		{Secret4Name, true, "Protected secret should be kept"},
		{Secret5Name, false, "Unused secret should be deleted, even if annotations are nil"},
	}

	janitor := suite.buildJanitor(suite.clientBuilder.Build())
	err := janitor.CleanUnusedSecrets()

	suite.NoError(err)

	for _, tt := range expected {
		suite.Run(tt.reason, func() {
			actual := &corev1.Secret{}
			err = janitor.Client.Get(context.Background(), client.ObjectKey{
				Namespace: Namespace,
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
		expected     error
	}{
		{
			name: "TestErrorGettingSecrets",
			interactions: []interaction{
				{
					"List",
					[]interface{}{mock.Anything, mock.AnythingOfType("*v1.SecretList"), mock.AnythingOfType("client.MatchingLabels")},
					[]interface{}{fmt.Errorf("api error")},
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
					[]interface{}{mock.Anything, mock.AnythingOfType("*v1.SecretList"), mock.AnythingOfType("client.MatchingLabels")},
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
			expected: fmt.Errorf("failed to retrieve list of pods: api error"),
		},
		{
			name: "TestErrorDeletingSecret",
			interactions: []interaction{
				{
					"List",
					[]interface{}{mock.Anything, mock.AnythingOfType("*v1.SecretList"), mock.AnythingOfType("client.MatchingLabels")},
					[]interface{}{nil},
					func(arguments mock.Arguments) {
						if secretList, ok := arguments.Get(1).(*corev1.SecretList); ok {
							secretList.Items = []corev1.Secret{*makeSecret(Secret1Name, secret.AivenatorSecretType)}
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
			expected: fmt.Errorf("failed to delete secret: api error"),
		},
		{
			name: "TestSecretNotFoundWhenDeleting",
			interactions: []interaction{
				{
					"List",
					[]interface{}{mock.Anything, mock.AnythingOfType("*v1.SecretList"), mock.AnythingOfType("client.MatchingLabels")},
					[]interface{}{nil},
					func(arguments mock.Arguments) {
						if secretList, ok := arguments.Get(1).(*corev1.SecretList); ok {
							secretList.Items = []corev1.Secret{*makeSecret(Secret1Name, secret.AivenatorSecretType)}
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
			expected: nil,
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
			err := janitor.CleanUnusedSecrets()

			suite.Equal(tt.expected, err)
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

func makeSecret(name, secretType string, optFuncs ...MakeSecretOption) *corev1.Secret {
	opts := &makeSecretOpts{}
	for _, optFunc := range optFuncs {
		optFunc(opts)
	}
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: Namespace,
			Labels: map[string]string{
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
