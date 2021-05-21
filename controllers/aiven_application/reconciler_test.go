package aiven_application

import (
	"context"
	"github.com/nais/aivenator/pkg/credentials"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

func TestAivenApplicationReconciler_NeedsSynchronization(t *testing.T) {
	tests := []struct {
		name        string
		application aiven_nais_io_v1.AivenApplication
		hasSecret   bool
		want        bool
		wantErr     bool
	}{
		{
			name:        "EmptyApplication",
			application: aiven_nais_io_v1.AivenApplication{},
			want:        true,
		},
		{
			name:        "BaseApplication",
			application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").Build(),
			want:        true,
		},
		{
			name: "ChangedApplication",
			application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
				WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: "123"}).
				Build(),
			want: true,
		},
		{
			name: "UnchangedApplication",
			application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: "my-secret-name"}).
				WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: "7884e7801510de37"}).
				Build(),
			hasSecret: true,
		},
		{
			name: "UnchangedApplicationButSecretMissing",
			application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: "my-secret-name"}).
				WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: "7884e7801510de37"}).
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
				Logger:  log.NewEntry(log.New()),
				Manager: credentials.Manager{},
			}

			hash, err := tt.application.Hash()
			if err != nil {
				t.Errorf("Failed to generate hash: %s", err)
				return
			}
			got, err := r.NeedsSynchronization(ctx, tt.application, hash, r.Logger)
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
