package secret

import (
	"github.com/nais/aivenator/constants"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	nais_io_v1alpha1 "github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"testing"
	"time"
)

func TestHandler_Apply(t *testing.T) {
	type args struct {
		application aiven_nais_io_v1.AivenApplication
		secret      corev1.Secret
		assert      func(*testing.T, args)
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "BaseApplication",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
					Build(),
				secret: corev1.Secret{},
				assert: func(t *testing.T, a args) {
					assert.Equal(t, constants.AivenatorSecretType, a.secret.Labels[constants.SecretTypeLabel])
					assert.Equal(t, a.application.GetName(), a.secret.Labels[constants.AppLabel])
					assert.Equal(t, a.application.GetNamespace(), a.secret.Labels[constants.TeamLabel])
					assert.Equal(t, a.application.GetNamespace(), a.secret.GetNamespace())
				},
			},
		},
		{
			name: "ApplicationWithSecretAndCorrelationId",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: "my-secret"}).
					WithAnnotation(nais_io_v1alpha1.DeploymentCorrelationIDAnnotation, "correlation-id").
					Build(),
				secret: corev1.Secret{},
				assert: func(t *testing.T, a args) {
					assert.Equal(t, "correlation-id", a.secret.GetAnnotations()[nais_io_v1alpha1.DeploymentCorrelationIDAnnotation])
					assert.Equal(t, a.application.Spec.SecretName, a.secret.GetName())
				},
			},
		},
		{
			name: "OwnerReference",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
					Build(),
				secret: corev1.Secret{},
				assert: func(t *testing.T, a args) {
					assert.Equal(t, a.application.GetName(), a.secret.GetOwnerReferences()[0].Name)
					assert.Equal(t, a.application.Kind, a.secret.GetOwnerReferences()[0].Kind)
					assert.Equal(t, a.application.APIVersion, a.secret.GetOwnerReferences()[0].APIVersion)
				},
			},
		},
		{
			name: "ProtectedSecret",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: "my-secret", Protected: true}).
					Build(),
				secret: corev1.Secret{},
				assert: func(t *testing.T, a args) {
					assert.Equal(t, "true", a.secret.GetAnnotations()[constants.AivenatorProtectedAnnotation])
				},
			},
		},
		{
			name: "HasTimestamp",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
					Build(),
				secret: corev1.Secret{},
				assert: func(t *testing.T, a args) {
					value := a.secret.StringData[AivenSecretUpdatedKey]
					timestamp, err := time.Parse(time.RFC3339, value)
					assert.NoError(t, err)
					assert.WithinDuration(t, time.Now(), timestamp, time.Second*10)
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Handler{}
			assert.NoError(t, s.Apply(&tt.args.application, &tt.args.secret, nil))
			tt.args.assert(t, tt.args)
		})
	}
}
