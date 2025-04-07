package credentials

import (
	"context"
	"fmt"
	"testing"

	"github.com/nais/aivenator/pkg/handlers/secret"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	test_logger "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestManager_Apply
func TestManager_Apply(t *testing.T) {
	// given
	mockHandler := MockHandler{}
	mockSecretsHandler := secret.MockSecrets{}
	expectedAnnotations := make(map[string]string)
	expectedAnnotations["one"] = "1"
	expectedSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: expectedAnnotations,
		},
	}
	appliedSecrets := []*corev1.Secret{&expectedSecret}
	mockSecretsHandler.On("GetOrInitSecret", mock.Anything, mock.Anything, mock.Anything).Return(expectedSecret)
	mockHandler.
		On("Apply",
			mock.Anything,
			mock.AnythingOfType("*aiven_nais_io_v1.AivenApplication"),
			mock.Anything).
		Return(appliedSecrets, nil)
	application := aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").Build()
	manager := Manager{handlers: []Handler{&mockHandler}}

	// when
	logger, _ := test_logger.NewNullLogger()
	secrets, err := manager.CreateSecret(context.Background(), &application, &corev1.Secret{}, logger)

	// then
	assert.NoError(t, err)
	assert.NotEmpty(t, secrets)
	for _, s := range secrets {
		assert.Equal(t, s.ObjectMeta.Annotations, expectedAnnotations)
	}
}

func TestManager_ApplyFailed(t *testing.T) {
	// given
	mockHandler := MockHandler{}
	failingHandler := MockHandler{}
	expectedAnnotations := make(map[string]string)
	expectedAnnotations["one"] = "1"
	mockHandler.
		On("Apply",
			mock.Anything,
			mock.AnythingOfType("*aiven_nais_io_v1.AivenApplication"),
			mock.Anything).
		Return([]*corev1.Secret{}, nil)
	mockHandler.
		On("Cleanup",
			mock.Anything,
			mock.AnythingOfType("*v1.Secret"),
			mock.Anything).
		Return(nil)

	handlerError := fmt.Errorf("failing handler")

	failingHandler.
		On("Apply",
			mock.Anything,
			mock.AnythingOfType("*aiven_nais_io_v1.AivenApplication"),
			mock.Anything).
		Return(nil, handlerError)
	failingHandler.
		On("Cleanup",
			mock.Anything,
			mock.AnythingOfType("*v1.Secret"),
			mock.Anything).
		Return(nil)
	application := aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").Build()
	manager := Manager{handlers: []Handler{&mockHandler, &failingHandler}}

	// when
	secret := &corev1.Secret{}
	logger, _ := test_logger.NewNullLogger()
	_, err := manager.CreateSecret(context.Background(), &application, secret, logger)

	// then
	assert.Error(t, err)
	assert.EqualError(t, err, handlerError.Error())
}

func TestManager_Cleanup(t *testing.T) {
	// given
	mockHandler := MockHandler{}
	mockHandler.
		On("Cleanup",
			mock.Anything,
			mock.AnythingOfType("*v1.Secret"),
			mock.Anything).
		Return(nil)
	secret := corev1.Secret{}
	manager := Manager{handlers: []Handler{&mockHandler}}

	// when
	err := manager.Cleanup(context.Background(), &secret, nil)

	// then
	assert.NoError(t, err)
	mockHandler.AssertCalled(t, "Cleanup",
		mock.Anything,
		mock.AnythingOfType("*v1.Secret"),
		mock.Anything)
}
