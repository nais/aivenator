package credentials

import (
	"context"
	"fmt"
	"github.com/nais/aivenator/pkg/mocks"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	"testing"
)

func TestManager_Apply(t *testing.T) {
	// given
	mockHandler := mocks.Handler{}
	expectedAnnotations := make(map[string]string)
	expectedAnnotations["one"] = "1"
	mockHandler.
		On("Apply",
			mock.Anything,
			mock.AnythingOfType("*aiven_nais_io_v1.AivenApplication"),
			mock.AnythingOfType("*v1.Secret"),
			mock.Anything).
		Return(nil).
		Run(func(args mock.Arguments) {
			secret := args.Get(2).(*corev1.Secret)
			secret.ObjectMeta.Annotations = make(map[string]string, len(expectedAnnotations))
			for key, value := range expectedAnnotations {
				secret.ObjectMeta.Annotations[key] = value
			}
		})
	application := aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").Build()
	manager := Manager{handlers: []Handler{&mockHandler}}

	// when
	secret := &corev1.Secret{}
	secret, err := manager.CreateSecret(context.Background(), &application, secret, nil)

	// then
	assert.NoError(t, err)
	assert.Equal(t, secret.ObjectMeta.Annotations, expectedAnnotations)
}

func TestManager_ApplyFailed(t *testing.T) {
	// given
	mockHandler := mocks.Handler{}
	failingHandler := mocks.Handler{}
	expectedAnnotations := make(map[string]string)
	expectedAnnotations["one"] = "1"
	mockHandler.
		On("Apply",
			mock.Anything,
			mock.AnythingOfType("*aiven_nais_io_v1.AivenApplication"),
			mock.AnythingOfType("*v1.Secret"),
			mock.Anything).
		Return(nil).
		Run(func(args mock.Arguments) {
			secret := args.Get(2).(*corev1.Secret)
			secret.ObjectMeta.Annotations = make(map[string]string, len(expectedAnnotations))
			for key, value := range expectedAnnotations {
				secret.ObjectMeta.Annotations[key] = value
			}
		})
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
			mock.AnythingOfType("*v1.Secret"),
			mock.Anything).
		Return(handlerError)
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
	_, err := manager.CreateSecret(context.Background(), &application, secret, nil)

	// then
	assert.Error(t, err)
	assert.EqualError(t, err, handlerError.Error())
	mockHandler.AssertCalled(t, "Cleanup",
		mock.Anything,
		mock.AnythingOfType("*v1.Secret"),
		mock.Anything)
}

func TestManager_Cleanup(t *testing.T) {
	// given
	mockHandler := mocks.Handler{}
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
