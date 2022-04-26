package aiven_application

import (
	"context"
	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/credentials"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
	"time"
)

func TestAivenApplicationReconciler_NeedsSynchronization(t *testing.T) {
	tests := []struct {
		name              string
		application       aiven_nais_io_v1.AivenApplication
		hasSecret         bool
		hasOwnerReference bool
		isProtected       bool
		want              bool
		wantErr           bool
	}{
		{
			name:              "EmptyApplication",
			application:       aiven_nais_io_v1.AivenApplication{},
			hasSecret:         false,
			hasOwnerReference: false,
			isProtected:       false,
			want:              true,
			wantErr:           false,
		},
		{
			name:              "BaseApplication",
			application:       aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").Build(),
			hasSecret:         false,
			hasOwnerReference: false,
			isProtected:       false,
			want:              true,
			wantErr:           false,
		},
		{
			name: "ChangedApplication",
			application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
				WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: "123"}).
				Build(),
			hasSecret:         false,
			hasOwnerReference: false,
			isProtected:       false,
			want:              true,
			wantErr:           false,
		},
		{
			name: "UnchangedApplication",
			application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: "my-secret-name"}).
				WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: "4264acf8ec09e93"}).
				Build(),
			hasSecret:         true,
			hasOwnerReference: true,
			isProtected:       false,
			want:              false,
			wantErr:           false,
		},
		{
			name: "UnchangedApplicationButSecretMissing",
			application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: "my-secret-name"}).
				WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: "4264acf8ec09e93"}).
				Build(),
			hasSecret:         false,
			hasOwnerReference: false,
			isProtected:       false,
			want:              true,
			wantErr:           false,
		},
		{
			name: "UnchangedApplicationMissingReplicaSetOwnerReference",
			application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: "my-secret-name"}).
				WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: "4264acf8ec09e93"}).
				Build(),
			hasSecret:         true,
			hasOwnerReference: false,
			isProtected:       false,
			want:              true,
			wantErr:           false,
		},
		{
			name: "ProtectedApplicationMissingReplicaSetOwnerReference",
			application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: "my-secret-name"}).
				WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: "4264acf8ec09e93"}).
				Build(),
			hasSecret:         true,
			hasOwnerReference: false,
			isProtected:       true,
			want:              false,
			wantErr:           false,
		},
		{
			name: "ProtectedApplication",
			application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: "my-secret-name"}).
				Build(),
			hasSecret:         false,
			hasOwnerReference: false,
			isProtected:       true,
			want:              true,
			wantErr:           false,
		},
	}

	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientBuilder := fake.NewClientBuilder()
			if tt.hasSecret {
				ownerReferences := make([]metav1.OwnerReference, 0)
				if tt.hasOwnerReference {
					ownerReferences = append(ownerReferences, metav1.OwnerReference{Kind: "ReplicaSet"})
				}
				annotations := make(map[string]string)
				annotations[nais_io_v1.DeploymentCorrelationIDAnnotation] = "a-correlation-id"
				if tt.isProtected {
					annotations[constants.AivenatorProtectedAnnotation] = "true"
				}
				clientBuilder.WithRuntimeObjects(&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "my-secret-name",
						Namespace:       "ns",
						OwnerReferences: ownerReferences,
						Annotations:     annotations,
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
				t.Errorf("NeedsSynchronization() got = %v, want %v; actual hash: %v", got, tt.want, hash)
			}
		})
	}
}

func TestAivenApplicationReconciler_HandleProtectedAndTimeLimited(t *testing.T) {
	var scheme = runtime.NewScheme()

	err := aiven_nais_io_v1.AddToScheme(scheme)
	if err != nil {
		panic(err)
	}

	tests := []struct {
		name        string
		application aiven_nais_io_v1.AivenApplication
		hasSecret   bool
		wantErr     bool
		deleted     bool
	}{
		{
			name: "ApplicationWhereTimeLimitIsExceededAndWhereSecretIsDeleted",
			application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: "my-secret-name", ExpiresAt: &metav1.Time{Time: time.Now().AddDate(0, 0, -2)}}).
				WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: "4264acf8ec09e93"}).
				Build(),
			hasSecret: false,
			deleted:   true,
		},
		{
			name: "ApplicationWhereTimeLimitIsStillValidAndWhereSecretIsDeleted",
			application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: "my-secret-name", ExpiresAt: &metav1.Time{Time: time.Now().AddDate(0, 0, 2)}}).
				WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: "4264acf8ec09e93"}).
				Build(),
			hasSecret: false,
			deleted:   false,
		},
	}

	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			clientBuilder.WithRuntimeObjects(&tt.application)
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

			applicationDeleted, err := r.HandleProtectedAndTimeLimited(ctx, tt.application, r.Logger)
			if (err != nil) != tt.wantErr {
				t.Errorf("HandleProtectedAndTimeLimited() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if applicationDeleted != tt.deleted {
				t.Errorf("HandleProtectedAndTimeLimited()  actual result; applicationDeleted = %v, deleted %v", applicationDeleted, tt.deleted)
			}
		})
	}
}
