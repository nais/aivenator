package credentials

import (
	"context"
	kafka_nais_io_v1 "github.com/nais/liberator/pkg/apis/kafka.nais.io/v1"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

func TestAivenApplicationReconciler_NeedsSynchronization(t *testing.T) {
	tests := []struct {
		name        string
		application kafka_nais_io_v1.AivenApplication
		hasSecret   bool
		want        bool
		wantErr     bool
	}{
		{
			name:        "EmptyApplication",
			application: kafka_nais_io_v1.AivenApplication{},
			want:        true,
		},
		{
			name:        "BaseApplication",
			application: kafka_nais_io_v1.NewAivenApplicationBuilder("app", "ns").Build(),
			want:        true,
		},
		{
			name: "ChangedApplication",
			application: kafka_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
				WithStatus(kafka_nais_io_v1.AivenApplicationStatus{SynchronizationHash: "123"}).
				Build(),
			want: true,
		},
		{
			name: "UnchangedApplication",
			application: kafka_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
				WithSpec(kafka_nais_io_v1.AivenApplicationSpec{SecretName: "my-secret-name"}).
				WithStatus(kafka_nais_io_v1.AivenApplicationStatus{SynchronizationHash: "7884e7801510de37"}).
				Build(),
			hasSecret: true,
		},
		{
			name: "UnchangedApplicationButSecretMissing",
			application: kafka_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
				WithSpec(kafka_nais_io_v1.AivenApplicationSpec{SecretName: "my-secret-name"}).
				WithStatus(kafka_nais_io_v1.AivenApplicationStatus{SynchronizationHash: "7884e7801510de37"}).
				Build(),
			want: true,
		},
	}

	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientBuilder := fake.NewClientBuilder()
			if tt.hasSecret {
				clientBuilder.WithRuntimeObjects(&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-secret-name",
						Namespace: "ns",
					},
				})
			}
			r := AivenApplicationReconciler{
				Client:  clientBuilder.Build(),
				Logger:  log.New(),
				Creator: Creator{},
			}

			got, err := r.NeedsSynchronization(ctx, tt.application, log.NewEntry(r.Logger))
			if (err != nil) != tt.wantErr {
				t.Errorf("NeedsSynchronization() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("NeedsSynchronization() got = %v, want %v", got, tt.want)
			}
		})
	}
}
