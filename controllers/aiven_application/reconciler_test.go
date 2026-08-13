package aiven_application

import (
	"context"
	"errors"
	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/credentials"
	"github.com/nais/aivenator/pkg/metrics"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	nais_io_v1alpha1 "github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"testing"
	"time"
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
	var scheme = runtime.NewScheme()

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
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName, Kafka: &aiven_nais_io_v1.KafkaSpec{SecretName: secretName}}).
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
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName, Kafka: &aiven_nais_io_v1.KafkaSpec{SecretName: secretName}}).
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
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName, Kafka: &aiven_nais_io_v1.KafkaSpec{SecretName: secretName}}).
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

			// make status hash align with the current spec for the unchanged scenarios
			if tt.name == "UnchangedApplication" || tt.name == "UnchangedApplicationButSecretMissing" || tt.name == "ProtectedApplication" {
				tt.args.application.Status.SynchronizationHash = hash
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

// notConvergedSeries reads the app's gauge presence without creating the series.
func notConvergedSeries(t *testing.T) int {
	t.Helper()
	return testutil.CollectAndCount(metrics.AppNotConverged)
}

func TestAivenApplicationReconciler_NotConvergedGauge(t *testing.T) {
	scheme := setupScheme()
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: appName}}
	gauge := prometheus.Labels{metrics.LabelNamespace: namespace, metrics.LabelAivenApp: appName}

	t.Run("FailedReconcileIncrementsTheAppSeries", func(t *testing.T) {
		defer metrics.AppNotConverged.Reset()
		r := AivenApplicationReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
						return errors.New("apiserver unavailable")
					},
				}).Build(),
			Logger:  log.NewEntry(log.New()),
			Manager: credentials.Manager{},
		}
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
		if got := testutil.ToFloat64(metrics.AppNotConverged.With(gauge)); got != 1 {
			t.Errorf("gauge = %v, want 1", got)
		}
	})

	t.Run("SuccessfulReconcileRemovesTheAppSeries", func(t *testing.T) {
		defer metrics.AppNotConverged.Reset()
		app := aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace).Build()
		r := AivenApplicationReconciler{
			Client:     fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(&app).WithStatusSubresource(&app).Build(),
			Logger:     log.NewEntry(log.New()),
			Manager:    credentials.Manager{},
			appChanges: make(chan aiven_nais_io_v1.AivenApplication, 1),
		}
		metrics.AppNotConverged.With(gauge).Inc() // series from an earlier failing reconcile
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
		if got := notConvergedSeries(t); got != 0 {
			t.Errorf("series count = %v, want 0", got)
		}
	})

	t.Run("DeletedAppRemovesTheAppSeries", func(t *testing.T) {
		defer metrics.AppNotConverged.Reset()
		r := AivenApplicationReconciler{
			Client:  fake.NewClientBuilder().WithScheme(scheme).Build(),
			Logger:  log.NewEntry(log.New()),
			Manager: credentials.Manager{},
		}
		metrics.AppNotConverged.With(gauge).Inc() // series from an earlier failing reconcile
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
		if got := notConvergedSeries(t); got != 0 {
			t.Errorf("series count = %v, want 0", got)
		}
	})
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
