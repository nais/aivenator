package credentials

import (
	"github.com/nais/aivenator/pkg/mocks"
	kafka_nais_io_v1 "github.com/nais/liberator/pkg/apis/kafka.nais.io/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	"testing"
)

func TestCreator_Apply(t *testing.T) {
	// given
	mockHandler := mocks.Handler{}
	expectedAnnotations := make(map[string]string)
	expectedAnnotations["one"] = "1"
	mockHandler.
		On("Apply", mock.AnythingOfType("*kafka_nais_io_v1.AivenApplication"), mock.AnythingOfType("*v1.Secret"), mock.Anything).
		Return(nil).
		Run(func(args mock.Arguments) {
			secret := args.Get(1).(*corev1.Secret)
			secret.ObjectMeta.Annotations = make(map[string]string, len(expectedAnnotations))
			for key, value := range expectedAnnotations {
				secret.ObjectMeta.Annotations[key] = value
			}
		})
	application := kafka_nais_io_v1.NewAivenApplicationBuilder("app", "ns").Build()
	c := Creator{handlers: []Handler{&mockHandler}}

	// when
	secret, err := c.CreateSecret(&application, nil)

	// then
	assert.NoError(t, err)
	assert.Equal(t, secret.ObjectMeta.Annotations, expectedAnnotations)
}
