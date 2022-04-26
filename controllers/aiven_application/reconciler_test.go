package aiven_application

import (
	"context"
	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/credentials"
	"github.com/nais/aivenator/pkg/utils"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	nais_io_v1alpha1 "github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
	"time"
)

type schemeAdders func(s *runtime.Scheme) error

func setupScheme() *runtime.Scheme {
	var scheme = runtime.NewScheme()

	adders := []schemeAdders{
		metav1.AddMetaToScheme,
		corev1.AddToScheme,
		appsv1.AddToScheme,
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
		hasRSOwner  bool
		hasAppOwner bool
		hasJobOwner bool
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
				hasRSOwner:  false,
				hasAppOwner: false,
				hasJobOwner: false,
				isProtected: false,
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "BaseApplication",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").Build(),
				hasSecret:   false,
				hasRSOwner:  false,
				hasAppOwner: false,
				hasJobOwner: false,
				isProtected: false,
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "ChangedApplication",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
					WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: "123"}).
					Build(),
				hasSecret:   false,
				hasRSOwner:  true,
				hasAppOwner: false,
				hasJobOwner: false,
				isProtected: false,
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "UnchangedApplication",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: "my-secret-name"}).
					WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: "4264acf8ec09e93"}).
					Build(),
				hasSecret:   true,
				hasRSOwner:  true,
				hasAppOwner: false,
				hasJobOwner: false,
				isProtected: false,
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "UnchangedApplicationButSecretMissing",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: "my-secret-name"}).
					WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: "4264acf8ec09e93"}).
					Build(),
				hasSecret:   false,
				hasRSOwner:  false,
				hasAppOwner: true,
				hasJobOwner: false,
				isProtected: false,
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "UnchangedApplicationMissingReplicaSetOwnerReference",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: "my-secret-name"}).
					WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: "4264acf8ec09e93"}).
					Build(),
				hasSecret:   true,
				hasRSOwner:  false,
				hasAppOwner: true,
				hasJobOwner: false,
				isProtected: false,
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "UnchangedNaisJobMissingReplicaSetOwnerReference",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: "my-secret-name"}).
					WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: "4264acf8ec09e93"}).
					Build(),
				hasSecret:   true,
				hasRSOwner:  false,
				hasAppOwner: false,
				hasJobOwner: true,
				isProtected: false,
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "ProtectedApplicationMissingReplicaSetOwnerReference",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: "my-secret-name"}).
					WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: "4264acf8ec09e93"}).
					Build(),
				hasSecret:   true,
				hasRSOwner:  false,
				hasAppOwner: false,
				hasJobOwner: false,
				isProtected: true,
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "ProtectedApplication",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: "my-secret-name"}).
					Build(),
				hasSecret:   false,
				hasRSOwner:  false,
				hasAppOwner: false,
				hasJobOwner: false,
				isProtected: true,
			},
			want:    true,
			wantErr: false,
		},
	}

	ctx := context.Background()

	rsKind, err := utils.GetGVK(scheme, &appsv1.ReplicaSet{})
	if err != nil {
		panic(err)
	}
	appKind, err := utils.GetGVK(scheme, &nais_io_v1alpha1.Application{})
	if err != nil {
		panic(err)
	}
	jobKind, err := utils.GetGVK(scheme, &nais_io_v1.Naisjob{})
	if err != nil {
		panic(err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.args.hasSecret {
				ownerReferences := make([]metav1.OwnerReference, 0)
				if tt.args.hasRSOwner {
					ownerReferences = append(ownerReferences, metav1.OwnerReference{Kind: rsKind.Kind})
				}
				if tt.args.hasAppOwner {
					ownerReferences = append(ownerReferences, metav1.OwnerReference{Kind: appKind.Kind})
				}
				if tt.args.hasJobOwner {
					ownerReferences = append(ownerReferences, metav1.OwnerReference{Kind: jobKind.Kind})
				}
				annotations := make(map[string]string)
				annotations[nais_io_v1.DeploymentCorrelationIDAnnotation] = "a-correlation-id"
				if tt.args.isProtected {
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
