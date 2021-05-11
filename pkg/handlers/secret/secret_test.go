package secret

import (
	kafka_nais_io_v1 "github.com/nais/liberator/pkg/apis/kafka.nais.io/v1"
	nais_io_v1alpha1 "github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"testing"
)

func TestHandler_Apply(t *testing.T) {
	type args struct {
		application kafka_nais_io_v1.AivenApplication
		secret      corev1.Secret
		assert      func(args)
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "BaseApplication",
			args: args{
				application: kafka_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
					Build(),
				secret: corev1.Secret{},
				assert: func(a args) {
					assert.Equal(t, "", a.secret.GetName())
					assert.Equal(t, AivenatorSecretType, a.secret.Labels[SecretTypeLabel])
					assert.Equal(t, a.application.GetNamespace(), a.secret.Labels[TeamLabel])
					assert.Equal(t, a.application.GetNamespace(), a.secret.GetNamespace())
				},
			},
		},
		{
			name: "ApplicationWithSecretAndCorrelationId",
			args: args{
				application: kafka_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
					WithSpec(kafka_nais_io_v1.AivenApplicationSpec{SecretName: "my-secret"}).
					WithAnnotation(nais_io_v1alpha1.DeploymentCorrelationIDAnnotation, "correlation-id").
					Build(),
				secret: corev1.Secret{},
				assert: func(a args) {
					assert.Equal(t, "correlation-id", a.secret.GetAnnotations()[nais_io_v1alpha1.DeploymentCorrelationIDAnnotation])
					assert.Equal(t, a.application.Spec.SecretName, a.secret.GetName())
				},
			},
		},
		{
			name: "OwnerReference",
			args: args{
				application: kafka_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
					Build(),
				secret: corev1.Secret{},
				assert: func(a args) {
					assert.Equal(t, a.application.GetName(), a.secret.GetOwnerReferences()[0].Name)
					assert.Equal(t, a.application.Kind, a.secret.GetOwnerReferences()[0].Kind)
					assert.Equal(t, a.application.APIVersion, a.secret.GetOwnerReferences()[0].APIVersion)
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Handler{}
			assert.NoError(t, s.Apply(&tt.args.application, &tt.args.secret, nil))
			tt.args.assert(tt.args)
		})
	}
}
