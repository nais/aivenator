package aiven_application

import (
	"context"
	"testing"
	"time"

	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/credentials"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	nais_io_v1alpha1 "github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	appName       = "app"
	namespace     = "ns"
	secretName    = "my-secret-name"
	syncHash      = "4264acf8ec09e93"
	correlationId = "a-correlation-id"
)

type schemeAdders func(s *runtime.Scheme) error

func setupScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()

	adders := []schemeAdders{
		metav1.AddMetaToScheme,
		corev1.AddToScheme,
		appsv1.AddToScheme,
		batchv1.AddToScheme,
		aiven_nais_io_v1.AddToScheme,
		nais_io_v1.AddToScheme,
		nais_io_v1alpha1.AddToScheme,
	}

	for _, f := range adders {
		err := f(scheme)
		if err != nil {
			panic(err)
		}
	}
	return scheme
}

func TestAivenApplicationReconciler_NeedsSynchronization(t *testing.T) {
	scheme := setupScheme()

	type args struct {
		application aiven_nais_io_v1.AivenApplication
		hasSecret   bool
		isProtected bool
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{
			name: "EmptyApplication",
			args: args{
				application: aiven_nais_io_v1.AivenApplication{},
				hasSecret:   false,
				isProtected: false,
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "BaseApplication",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace).Build(),
				hasSecret:   false,
				isProtected: false,
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "ChangedApplication",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace).
					WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: "123"}).
					Build(),
				hasSecret:   false,
				isProtected: false,
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "UnchangedApplication",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace).
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName}).
					WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: syncHash}).
					Build(),
				hasSecret:   true,
				isProtected: false,
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "UnchangedApplicationButSecretMissing",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace).
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName}).
					WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: syncHash}).
					Build(),
				hasSecret:   false,
				isProtected: false,
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "ProtectedApplication",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace).
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName}).
					Build(),
				hasSecret:   false,
				isProtected: true,
			},
			want:    true,
			wantErr: false,
		},
	}

	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.args.hasSecret {
				ownerReferences := make([]metav1.OwnerReference, 0)
				labels := make(map[string]string)
				labels[constants.TeamLabel] = namespace
				annotations := make(map[string]string)
				annotations[nais_io_v1.DeploymentCorrelationIDAnnotation] = correlationId
				if tt.args.isProtected {
					annotations[constants.AivenatorProtectedKey] = "true"
					labels[constants.AivenatorProtectedKey] = "true"
				}
				clientBuilder.WithRuntimeObjects(&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:            secretName,
						Namespace:       namespace,
						OwnerReferences: ownerReferences,
						Annotations:     annotations,
						Labels:          labels,
					},
				})
			}
			r := AivenApplicationReconciler{
				Client:  clientBuilder.Build(),
				Logger:  log.NewEntry(log.New()),
				Manager: credentials.Manager{},
			}

			hash, err := tt.args.application.Hash()
			if err != nil {
				t.Errorf("Failed to generate hash: %s", err)
				return
			}
			got, err := r.NeedsSynchronization(ctx, tt.args.application, hash, r.Logger)
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
	scheme := setupScheme()

	tests := []struct {
		name        string
		application aiven_nais_io_v1.AivenApplication
		hasSecret   bool
		wantErr     bool
		deleted     bool
	}{
		{
			name: "ApplicationWhereTimeLimitIsExceededAndWhereSecretIsDeleted",
			application: aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace).
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName, ExpiresAt: &metav1.Time{Time: time.Now().AddDate(0, 0, -2)}}).
				WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: syncHash}).
				Build(),
			hasSecret: false,
			deleted:   true,
		},
		{
			name: "ApplicationWhereTimeLimitIsStillValidAndWhereSecretIsDeleted",
			application: aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace).
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName, ExpiresAt: &metav1.Time{Time: time.Now().AddDate(0, 0, 2)}}).
				WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: syncHash}).
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
						Name:      secretName,
						Namespace: namespace,
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
