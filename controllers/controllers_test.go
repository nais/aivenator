//go:build integration

package controllers_test

import (
	"context"
	"fmt"
	"github.com/aiven/aiven-go-client"
	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/controllers/aiven_application"
	"github.com/nais/aivenator/controllers/secrets"
	"github.com/nais/aivenator/pkg/credentials"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	"github.com/nais/liberator/pkg/crd"
	liberator_scheme "github.com/nais/liberator/pkg/scheme"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s_runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	"os"
	"path/filepath"
	"runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"testing"
	"time"
)

const (
	testProject          = "nav-integration-test"
	aivenApplicationName = "integration-test"
	baseSecretName       = "aiven-integration-test-secret"
	namespace            = "test-namespace"
	correlationId        = "correlation-id"
)

type testRig struct {
	t            *testing.T
	kubernetes   *envtest.Environment
	client       client.Client
	manager      ctrl.Manager
	synchronizer reconcile.Reconciler
	finalizer    reconcile.Reconciler
	scheme       *k8s_runtime.Scheme
}

func testBinDirectory() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "../.testbin/"))
}

func newTestRig(ctx context.Context, t *testing.T, logger *log.Logger) (*testRig, error) {
	err := os.Setenv("KUBEBUILDER_ASSETS", testBinDirectory())
	if err != nil {
		return nil, fmt.Errorf("failed to set environment variable: %w", err)
	}

	crdPath := crd.YamlDirectory()
	rig := &testRig{
		t: t,
		kubernetes: &envtest.Environment{
			CRDDirectoryPaths: []string{crdPath},
		},
	}

	t.Log("Starting Kubernetes")
	cfg, err := rig.kubernetes.Start()
	if err != nil {
		return nil, fmt.Errorf("setup Kubernetes test environment: %w", err)
	}

	t.Cleanup(func() {
		t.Log("Stopping Kubernetes")
		if err := rig.kubernetes.Stop(); err != nil {
			t.Errorf("failed to stop kubernetes test rig: %s", err)
		}
	})

	rig.scheme, err = liberator_scheme.All()
	if err != nil {
		return nil, fmt.Errorf("setup scheme: %w", err)
	}

	rig.manager, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme:             rig.scheme,
		MetricsBindAddress: "0",
	})
	if err != nil {
		return nil, fmt.Errorf("initialize manager: %w", err)
	}

	go func() {
		cache := rig.manager.GetCache()
		err := cache.Start(ctx)
		if err != nil {
			t.Errorf("unable to start informer cache: %v", err)
		}
	}()
	rig.client = rig.manager.GetClient()

	token, ok := os.LookupEnv("AIVEN_TOKEN")
	if !ok {
		return nil, fmt.Errorf("environment variable AIVEN_TOKEN must be set")
	}

	aivenClient, err := aiven.NewTokenClient(token, "")
	if err != nil {
		return nil, fmt.Errorf("unable to set up aiven client: %s", err)
	}

	credentialsManager := credentials.NewManager(ctx, aivenClient, []string{testProject}, testProject, logger.WithField("component", "CredentialsManager"))
	appChanges := make(chan aiven_nais_io_v1.AivenApplication)
	reconciler := aiven_application.NewReconciler(rig.manager, logger, credentialsManager, appChanges)

	err = reconciler.SetupWithManager(rig.manager)
	if err != nil {
		return nil, fmt.Errorf("setup synchronizer with manager: %w", err)
	}
	rig.synchronizer = &reconciler

	credentialsJanitor := credentials.Janitor{
		Client: rig.manager.GetClient(),
		Logger: logger.WithFields(log.Fields{
			"component": "AivenApplicationJanitor",
		}),
	}
	janitor := secrets.NewJanitor(credentialsJanitor, appChanges, logger)
	err = rig.manager.Add(janitor)
	if err != nil {
		return nil, fmt.Errorf("add janitor to manager: %w", err)
	}
	go func() {
		err := janitor.Start(ctx)
		if err != nil {
			logger.Errorf("unable to start secret janitor: %v", err)
			t.Errorf("unable to start secret janitor: %v", err)
		}
	}()

	finalizer := secrets.SecretsFinalizer{
		Client: rig.manager.GetClient(),
		Logger: logger.WithFields(log.Fields{
			"component": "AivenSecretsFinalizer",
		}),
		Manager: credentialsManager,
	}
	err = finalizer.SetupWithManager(rig.manager)
	if err != nil {
		return nil, fmt.Errorf("setup finalizer with manager: %w", err)
	}
	rig.finalizer = &finalizer

	return rig, nil
}

func (r testRig) assertExists(ctx context.Context, resource client.Object, objectKey client.ObjectKey) {
	r.t.Helper()
	err := r.client.Get(ctx, objectKey, resource)
	assert.NoError(r.t, err)
	assert.NotNil(r.t, resource)
}

func (r testRig) assertNotExists(ctx context.Context, resource client.Object, objectKey client.ObjectKey) {
	r.t.Helper()
	err := r.client.Get(ctx, objectKey, resource)
	assert.True(r.t, errors.IsNotFound(err), "the resource found in the cluster should not be there")
}

func (r testRig) createForTest(ctx context.Context, obj client.Object) {
	kind := obj.DeepCopyObject().GetObjectKind()
	r.t.Logf("Creating %s", describe(obj))
	if err := r.client.Create(ctx, obj); err != nil {
		r.t.Fatalf("resource %s cannot be persisted to fake Kubernetes: %s", describe(obj), err)
		return
	}
	// Create clears GVK for whatever reason, so we add it back here so we can continue to use this object
	obj.GetObjectKind().SetGroupVersionKind(kind.GroupVersionKind())
	r.t.Cleanup(func() {
		r.t.Logf("Deleting %s", describe(obj))
		if err := r.client.Delete(ctx, obj); err != nil {
			r.t.Errorf("failed to delete resource %s: %s", describe(obj), err)
		}
	})
}

func TestControllers(t *testing.T) {
	// Allow 15 seconds for test to complete
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Hour)
	t.Cleanup(cancel)

	logger := log.New()
	rig, err := newTestRig(ctx, t, logger)
	if err != nil {
		t.Errorf("unable to run controller integration tests: %s", err)
		t.FailNow()
	}

	rig.createForTest(ctx, &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	})

	wantedSecretName := fmt.Sprintf("%s-one", baseSecretName)

	app := aiven_nais_io_v1.NewAivenApplicationBuilder(aivenApplicationName, namespace).
		WithAnnotation(nais_io_v1.DeploymentCorrelationIDAnnotation, correlationId).
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{
			SecretName: wantedSecretName,
			Kafka:      &aiven_nais_io_v1.KafkaSpec{Pool: testProject},
		}).
		Build()

	appKey := types.NamespacedName{
		Namespace: app.Namespace,
		Name:      app.Name,
	}
	secretKey := types.NamespacedName{Name: wantedSecretName, Namespace: namespace}

	rig.assertNotExists(ctx, &aiven_nais_io_v1.AivenApplication{}, appKey)
	rig.assertNotExists(ctx, &v1.Secret{}, secretKey)

	rig.createForTest(ctx, &app)

	replicaSet := minimalReplicaSet("one", wantedSecretName)
	rig.createForTest(ctx, replicaSet)
	herringReplicaSet := minimalReplicaSet("two", fmt.Sprintf("%s-herring", baseSecretName))
	rig.createForTest(ctx, herringReplicaSet)

	rig.assertExists(ctx, &aiven_nais_io_v1.AivenApplication{}, appKey)

	result, err := rig.synchronizer.Reconcile(ctx, ctrl.Request{appKey})
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Check secret created with correct data
	secret := v1.Secret{}
	rig.assertExists(ctx, &secret, secretKey)
	actualFinalizers := secret.GetFinalizers()
	assert.Equal(t, 1, len(actualFinalizers))
	assert.Equal(t, constants.AivenatorFinalizer, actualFinalizers[0])

	err = rig.client.Delete(ctx, &secret)
	assert.NoError(t, err)
	time.Sleep(1 * time.Second) // Give time to stabilize
	result, err = rig.finalizer.Reconcile(ctx, ctrl.Request{secretKey})
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func minimalReplicaSet(suffix, secretName string) *appsv1.ReplicaSet {
	replicaSet := &appsv1.ReplicaSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ReplicaSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", aivenApplicationName, suffix),
			Namespace: namespace,
			Labels:    map[string]string{"app": aivenApplicationName},
			Annotations: map[string]string{
				nais_io_v1.DeploymentCorrelationIDAnnotation: correlationId,
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: pointer.Int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": aivenApplicationName},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name:      aivenApplicationName,
					Namespace: namespace,
					Labels:    map[string]string{"app": aivenApplicationName},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  aivenApplicationName,
							Image: "nginx:latest",
						},
					},
					Volumes: []v1.Volume{
						{
							Name: aiven_application.AivenVolumeName,
							VolumeSource: v1.VolumeSource{
								Secret: &v1.SecretVolumeSource{
									SecretName: secretName,
								},
							},
						},
					},
				},
			},
		},
	}
	return replicaSet
}

func describe(obj client.Object) string {
	gvk := obj.GetObjectKind().GroupVersionKind()
	return fmt.Sprintf("%s/%s/%s: %s/%s", gvk.Group, gvk.Version, gvk.Kind, obj.GetNamespace(), obj.GetName())
}
