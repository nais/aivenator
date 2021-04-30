package credentials

import (
	kafka_nais_io_v1 "github.com/nais/liberator/pkg/apis/kafka.nais.io/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
)

func TestCreator_Apply(t *testing.T) {
	// given
	mockHandler := MockHandler{}
	expectedAnnotations := make(map[string]string)
	expectedAnnotations["one"] = "1"
	mockHandler.
		On("Apply", mock.AnythingOfType("*kafka_nais_io_v1.AivenApplication"), mock.AnythingOfType("*v1.Secret")).
		Return(nil).
		Run(func(args mock.Arguments) {
			secret := args.Get(1).(*corev1.Secret)
			secret.ObjectMeta.Annotations = make(map[string]string, len(expectedAnnotations))
			for key, value := range expectedAnnotations {
				secret.ObjectMeta.Annotations[key] = value
			}
		})
	application := kafka_nais_io_v1.NewAivenApplicationBase("app", "ns")
	secret := corev1.Secret{ObjectMeta: v1.ObjectMeta{}}
	c := Creator{handlers: []Handler{&mockHandler}}

	// when
	err := c.Apply(&application, &secret)

	// then
	assert.NoError(t, err)
	assert.Equal(t, secret.ObjectMeta.Annotations, expectedAnnotations)
}
