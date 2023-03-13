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
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
	"time"
)

const (
	appName       = "app"
	namespace     = "ns"
	secretName    = "my-secret-name"
	syncHash      = "4264acf8ec09e93"
	correlationId = "a-correlation-id"
	rsName1       = "replicaset1"
	rsName2       = "replicaset2"
	cjName1       = "cronjob1"
	cjName2       = "cronjob2"
	jName1        = "job1"
	jName2        = "job2"
)

type schemeAdders func(s *runtime.Scheme) error

type identifier struct {
	GVK            schema.GroupVersionKind
	NamespacedName types.NamespacedName
}

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
		application     aiven_nais_io_v1.AivenApplication
		hasSecret       bool
		hasRSOwner      bool
		hasAppOwner     bool
		hasCronJobOwner bool
		isProtected     bool
		potentialOwners []client.Object
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
				application:     aiven_nais_io_v1.AivenApplication{},
				hasSecret:       false,
				hasRSOwner:      false,
				hasAppOwner:     false,
				hasCronJobOwner: false,
				isProtected:     false,
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "BaseApplication",
			args: args{
				application:     aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace).Build(),
				hasSecret:       false,
				hasRSOwner:      false,
				hasAppOwner:     false,
				hasCronJobOwner: false,
				isProtected:     false,
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
				hasSecret:       false,
				hasRSOwner:      true,
				hasAppOwner:     false,
				hasCronJobOwner: false,
				isProtected:     false,
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
				hasSecret:       true,
				hasRSOwner:      true,
				hasAppOwner:     false,
				hasCronJobOwner: false,
				isProtected:     false,
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
				hasSecret:       false,
				hasRSOwner:      false,
				hasAppOwner:     true,
				hasCronJobOwner: false,
				isProtected:     false,
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "UnchangedApplicationMissingReplicaSetOwnerReference",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace).
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName}).
					WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: syncHash}).
					Build(),
				hasSecret:       true,
				hasRSOwner:      false,
				hasAppOwner:     true,
				hasCronJobOwner: false,
				isProtected:     false,
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "UnchangedHasCronJobOwnerReference",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace).
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName}).
					WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: syncHash}).
					Build(),
				hasSecret:       true,
				hasRSOwner:      false,
				hasAppOwner:     false,
				hasCronJobOwner: true,
				isProtected:     false,
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "ProtectedApplicationMissingReplicaSetOwnerReference",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace).
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName}).
					WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: syncHash}).
					Build(),
				hasSecret:       true,
				hasRSOwner:      false,
				hasAppOwner:     false,
				hasCronJobOwner: false,
				isProtected:     true,
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "ProtectedApplication",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace).
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName}).
					Build(),
				hasSecret:       false,
				hasRSOwner:      false,
				hasAppOwner:     false,
				hasCronJobOwner: false,
				isProtected:     true,
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "ExistingObjectNotInOwnerReferences",
			args: args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace).
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName}).
					WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: syncHash}).
					Build(),
				hasSecret:       true,
				hasRSOwner:      false,
				hasAppOwner:     true,
				hasCronJobOwner: false,
				isProtected:     false,
				potentialOwners: []client.Object{
					makeReplicaSet(rsName1, correlationId, appName),
				},
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
	cronJobKind, err := utils.GetGVK(scheme, &batchv1.CronJob{})
	if err != nil {
		panic(err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.args.hasSecret {
				ownerReferences := make([]metav1.OwnerReference, 0)
				if tt.args.hasRSOwner {
					ownerReferences = append(ownerReferences, makeOwnerReference(rsName1, rsKind))
					clientBuilder.WithRuntimeObjects(makeReplicaSet(rsName1, correlationId, appName))
				}
				if tt.args.hasAppOwner {
					ownerReferences = append(ownerReferences, makeOwnerReference(appName, appKind))
					clientBuilder.WithRuntimeObjects(&nais_io_v1alpha1.Application{
						ObjectMeta: makeObjectMeta(appName, correlationId, appName),
					})
				}
				if tt.args.hasCronJobOwner {
					ownerReferences = append(ownerReferences, makeOwnerReference(cjName1, cronJobKind))
					clientBuilder.WithRuntimeObjects(makeCronJob(cjName1, correlationId, appName))
				}
				annotations := make(map[string]string)
				annotations[nais_io_v1.DeploymentCorrelationIDAnnotation] = correlationId
				if tt.args.isProtected {
					annotations[constants.AivenatorProtectedAnnotation] = "true"
				}
				clientBuilder.WithRuntimeObjects(&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:            secretName,
						Namespace:       namespace,
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
			got, err := r.NeedsSynchronization(ctx, tt.args.application, hash, tt.args.potentialOwners, r.Logger)
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

func TestAivenApplicationReconciler_FindDependentObjects(t *testing.T) {
	scheme := setupScheme()

	tests := []struct {
		name              string
		application       aiven_nais_io_v1.AivenApplication
		additionalObjects []client.Object
		wantedObjects     []identifier
	}{
		{
			name: "NoCorrelationId",
			application: aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace).
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName, ExpiresAt: &metav1.Time{Time: time.Now().AddDate(0, 0, -2)}}).
				WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: syncHash}).
				Build(),
		},
		{
			name: "NoReplicaSet",
			application: aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace).
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName, ExpiresAt: &metav1.Time{Time: time.Now().AddDate(0, 0, -2)}}).
				WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: syncHash}).
				WithAnnotation(nais_io_v1.DeploymentCorrelationIDAnnotation, correlationId).
				Build(),
		},
		{
			name: "FoundReplicaSet",
			application: aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace).
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName, ExpiresAt: &metav1.Time{Time: time.Now().AddDate(0, 0, 2)}}).
				WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: syncHash}).
				WithAnnotation(nais_io_v1.DeploymentCorrelationIDAnnotation, correlationId).
				Build(),
			additionalObjects: []client.Object{
				makeReplicaSet(rsName1, correlationId, appName),
				makeReplicaSet(rsName2, correlationId, appName),
			},
			wantedObjects: []identifier{
				makeIdentifier((&appsv1.ReplicaSet{}).GroupVersionKind(), rsName1),
				makeIdentifier((&appsv1.ReplicaSet{}).GroupVersionKind(), rsName2),
			},
		},
		{
			name: "FoundReplicaSetAmongMany",
			application: aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace).
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName, ExpiresAt: &metav1.Time{Time: time.Now().AddDate(0, 0, 2)}}).
				WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: syncHash}).
				WithAnnotation(nais_io_v1.DeploymentCorrelationIDAnnotation, correlationId).
				Build(),
			additionalObjects: []client.Object{
				makeReplicaSet(rsName1, correlationId, appName),
				makeReplicaSet("other-name", "other-correlation", appName),
				makeReplicaSet(rsName2, correlationId, appName),
			},
			wantedObjects: []identifier{
				makeIdentifier((&appsv1.ReplicaSet{}).GroupVersionKind(), rsName1),
				makeIdentifier((&appsv1.ReplicaSet{}).GroupVersionKind(), rsName2),
			},
		},
		{
			name: "FoundCronJob",
			application: aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace).
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName, ExpiresAt: &metav1.Time{Time: time.Now().AddDate(0, 0, 2)}}).
				WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: syncHash}).
				WithAnnotation(nais_io_v1.DeploymentCorrelationIDAnnotation, correlationId).
				Build(),
			additionalObjects: []client.Object{
				makeReplicaSet("other-name", "other-correlation", appName),
				makeCronJob(cjName1, correlationId, appName),
				makeCronJob(cjName2, correlationId, appName),
			},
			wantedObjects: []identifier{
				makeIdentifier((&batchv1.CronJob{}).GroupVersionKind(), cjName1),
				makeIdentifier((&batchv1.CronJob{}).GroupVersionKind(), cjName2),
			},
		},
		{
			name: "FoundJob",
			application: aiven_nais_io_v1.NewAivenApplicationBuilder(appName, namespace).
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName, ExpiresAt: &metav1.Time{Time: time.Now().AddDate(0, 0, 2)}}).
				WithStatus(aiven_nais_io_v1.AivenApplicationStatus{SynchronizationHash: syncHash}).
				WithAnnotation(nais_io_v1.DeploymentCorrelationIDAnnotation, correlationId).
				Build(),
			additionalObjects: []client.Object{
				makeReplicaSet("other-name", "other-correlation", appName),
				makeCronJob("other-name", "other-correlation", appName),
				makeJob(jName1, correlationId, appName),
				makeJob(jName2, correlationId, appName),
			},
			wantedObjects: []identifier{
				makeIdentifier((&batchv1.Job{}).GroupVersionKind(), jName1),
				makeIdentifier((&batchv1.Job{}).GroupVersionKind(), jName2),
			},
		},
	}

	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			clientBuilder.WithRuntimeObjects(&tt.application)
			if len(tt.additionalObjects) > 0 {
				clientBuilder.WithObjects(tt.additionalObjects...)
			}

			r := AivenApplicationReconciler{
				Client:  clientBuilder.Build(),
				Logger:  log.NewEntry(log.New()),
				Manager: credentials.Manager{},
			}

			result := r.FindDependentObjects(ctx, tt.application, r.Logger)
			if len(tt.wantedObjects) == 0 {
				if len(result) > 0 {
					t.Errorf("FindDependentObjects found object %v where none was wanted", result)
					return
				}
			} else {
				if len(result) == 0 {
					t.Errorf("FindDependentObjects found nothing, even though %v was expected", tt.wantedObjects)
					return
				}

				actuallyPresent := make(map[identifier]bool, len(tt.wantedObjects))
				for _, id := range tt.wantedObjects {
					actuallyPresent[id] = false
				}
				for _, object := range result {
					actual := makeIdentifierFromObject(object)
					for _, wantedObject := range tt.wantedObjects {
						if reflect.DeepEqual(actual, wantedObject) {
							actuallyPresent[actual] = true
						}
					}
				}
				for id, present := range actuallyPresent {
					if !present {
						t.Errorf("Missing object for identifier %v", id)
						return
					}
				}
			}
		})
	}
}

func makeOwnerReference(name string, gvk *schema.GroupVersionKind) metav1.OwnerReference {
	apiVersion, kind := gvk.ToAPIVersionAndKind()
	return metav1.OwnerReference{
		APIVersion: apiVersion,
		Kind:       kind,
		Name:       name,
	}
}

func makeIdentifierFromObject(obj client.Object) identifier {
	return identifier{
		GVK: obj.GetObjectKind().GroupVersionKind(),
		NamespacedName: types.NamespacedName{
			Namespace: obj.GetNamespace(),
			Name:      obj.GetName(),
		},
	}
}

func makeIdentifier(gvk schema.GroupVersionKind, name string) identifier {
	return identifier{
		GVK: gvk,
		NamespacedName: types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		},
	}
}

func makeObjectMeta(name string, correlationId string, appName string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      name,
		Namespace: namespace,
		Annotations: map[string]string{
			nais_io_v1.DeploymentCorrelationIDAnnotation: correlationId,
		},
		Labels: map[string]string{
			constants.AppLabel: appName,
		},
	}
}

func makeReplicaSet(rsName string, correlationId string, appName string) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{
		ObjectMeta: makeObjectMeta(rsName, correlationId, appName),
		Spec: appsv1.ReplicaSetSpec{
			Template: makePodTemplateSpec(),
		},
	}
}

func makeCronJob(cjName string, correlationId string, appName string) *batchv1.CronJob {
	return &batchv1.CronJob{
		ObjectMeta: makeObjectMeta(cjName, correlationId, appName),
		Spec: batchv1.CronJobSpec{
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: makePodTemplateSpec(),
				},
			},
		},
	}
}

func makeJob(jName string, correlationId string, appName string) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: makeObjectMeta(jName, correlationId, appName),
		Spec: batchv1.JobSpec{
			Template: makePodTemplateSpec(),
		},
	}
}

func makePodTemplateSpec() corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: AivenVolumeName,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: secretName,
						},
					},
				},
			},
		},
	}
}
