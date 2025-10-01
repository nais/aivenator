package aiven_application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nais/aivenator/pkg/credentials"
	"github.com/nais/aivenator/pkg/metrics"
	"github.com/nais/aivenator/pkg/utils"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	requeueInterval    = time.Second * 10
	secretWriteTimeout = time.Second * 5
	reconcileTimeout   = time.Minute * 2
	rolloutComplete    = "RolloutComplete"
	rolloutFailed      = "RolloutFailed"
	AivenVolumeName    = "aiven-credentials"
)

func NewReconciler(mgr manager.Manager, logger *log.Logger, credentialsManager credentials.Manager, appChanges chan<- aiven_nais_io_v1.AivenApplication, recorder events.EventRecorder) AivenApplicationReconciler {
	return AivenApplicationReconciler{
		Client:     mgr.GetClient(),
		Logger:     logger.WithFields(log.Fields{"component": "AivenApplicationReconciler"}),
		Manager:    credentialsManager,
		Recorder:   recorder,
		appChanges: appChanges,
	}
}

type AivenApplicationReconciler struct {
	client.Client
	Logger     log.FieldLogger
	Manager    credentials.Manager
	Recorder   events.EventRecorder
	appChanges chan<- aiven_nais_io_v1.AivenApplication
}

func (r *AivenApplicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var application aiven_nais_io_v1.AivenApplication

	ctx, cancel := context.WithTimeout(ctx, reconcileTimeout)
	defer cancel()

	logger := r.Logger.WithFields(log.Fields{
		"aiven_application": req.Name,
		"team":              req.Namespace,
	})

	logger.Infof("Processing request")
	defer func() {
		logger.Infof("Finished processing request")
		syncState := application.Status.SynchronizationState
		if syncState == "" {
			syncState = "unknown"
		}
		metrics.ApplicationsProcessed.With(prometheus.Labels{
			metrics.LabelSyncState: syncState,
		}).Inc()
	}()

	fail := func(err error) (ctrl.Result, error) {
		if err != nil {
			logger.Error(err)
		}
		application.Status.SynchronizationState = rolloutFailed
		cr := ctrl.Result{}

		if errors.Is(err, utils.ErrNotFound) {
			cr.RequeueAfter = requeueInterval * 10
		} else if !errors.Is(err, utils.ErrUnrecoverable) {
			cr.RequeueAfter = requeueInterval
		}

		if cr.RequeueAfter > 0 {
			metrics.ApplicationsRequeued.With(prometheus.Labels{
				metrics.LabelSyncState: rolloutFailed,
			}).Inc()
		}

		// Emit a warning event on the application if available
		if r.Recorder != nil && application.GetName() != "" && err != nil {
			r.Recorder.Eventf(&application, nil, corev1.EventTypeWarning, "SyncFailed", "Sync", "Sync failed: %v", err)
		}

		return cr, nil
	}

	err := r.Get(ctx, req.NamespacedName, &application)
	switch {
	case k8serrors.IsNotFound(err):
		return fail(fmt.Errorf("resource deleted from cluster; noop: %w", utils.ErrUnrecoverable))
	case err != nil:
		return fail(fmt.Errorf("unable to retrieve resource from cluster: %s", err))
	}
	// we now have the object; emit an event for visibility
	if r.Recorder != nil {
		r.Recorder.Eventf(&application, nil, corev1.EventTypeNormal, "Processing", "Reconciling", "Reconciling %s/%s", application.GetNamespace(), application.GetName())
	}
	logger = logger.WithField("app", application.Labels["app"])

	// mark as deprecated if we see Spec.SecretName, maybe degraded? v0v.
	// this would do well to be a version bump and a webhook but maybe that ship has sailed
	if application.Spec.SecretName != "" {
		deprecatedType := aiven_nais_io_v1.AivenApplicationConditionType("Deprecated")
		cond := application.Status.GetConditionOfType(deprecatedType)
		if cond == nil || cond.Status != corev1.ConditionTrue || cond.Reason != "DeprecatedField" {
			application.Status.AddCondition(aiven_nais_io_v1.AivenApplicationCondition{
				Type:    deprecatedType,
				Status:  corev1.ConditionTrue,
				Reason:  "DeprecatedField",
				Message: "Spec.SecretName is deprecated; needs resync via naiserator",
			})
			if r.Recorder != nil {
				r.Recorder.Eventf(&application, nil, corev1.EventTypeWarning, "DeprecatedSpecSecretName", "Deprecated", "Spec.SecretName is deprecated; migrate to Spec.{kafka,valkey,openSearch}.secretName")
			}
		}
	}

	applicationDeleted, err := r.HandleProtectedAndTimeLimited(ctx, application, logger)
	if err != nil {
		utils.LocalFail("HandleProtectedAndTimeLimited", &application, err, logger)
		if r.Recorder != nil && application.GetName() != "" {
			r.Recorder.Eventf(&application, nil, corev1.EventTypeWarning, "HandleProtectedFailed", "HandleProtected", "Failed handling protection/expiration: %v", err)
		}
		return fail(err)
	} else if applicationDeleted {
		if r.Recorder != nil && application.GetName() != "" {
			r.Recorder.Eventf(&application, nil, corev1.EventTypeNormal, "Deleted", "Delete", "Application deleted due to expiration")
		}
		return ctrl.Result{}, nil
	}

	logger.Infof("Application exists; processing")
	defer func() {
		application.Status.SynchronizationTime = &metav1.Time{Time: time.Now()}
		application.Status.ObservedGeneration = application.GetGeneration()
		err := metrics.ObserveKubernetesLatency("AivenApplication_Update", func() error {
			return r.Status().Update(ctx, &application)
		})
		if err != nil {
			metrics.KubernetesResourcesNotWritten.With(prometheus.Labels{
				metrics.LabelResourceType: "AivenApplication",
				metrics.LabelNamespace:    application.GetNamespace(),
			}).Inc()

			logger.Errorf("Unable to update status of application: %s\nWanted to save status: %+v", err, application.Status)
			if r.Recorder != nil {
				r.Recorder.Eventf(&application, nil, corev1.EventTypeWarning, "StatusUpdateFailed", "UpdateStatus", "Failed updating status: %v", err)
			}
		} else {
			metrics.KubernetesResourcesWritten.With(prometheus.Labels{
				metrics.LabelResourceType: "AivenApplication",
				metrics.LabelNamespace:    application.GetNamespace(),
			}).Inc()
			if r.Recorder != nil {
				r.Recorder.Eventf(&application, nil, corev1.EventTypeNormal, "StatusUpdated", "UpdateStatus", "Status updated: state=%s", application.Status.SynchronizationState)
			}
		}
	}()

	r.appChanges <- application

	// TODO: OpenSearch aivenapps are often manually created w/o naiserator deployment correlation ID
	logger = logger.WithField(nais_io_v1.DeploymentCorrelationIDAnnotation, application.GetAnnotations()[nais_io_v1.DeploymentCorrelationIDAnnotation])

	hash, err := application.Hash()
	if err != nil {
		utils.LocalFail("Hash", &application, err, logger)
		return fail(err)
	}

	needsSync, err := r.NeedsSynchronization(ctx, application, hash, logger)
	if err != nil {
		utils.LocalFail("NeedsSynchronization", &application, err, logger)
		return fail(err)
	}

	if !needsSync {
		return ctrl.Result{}, nil
	}

	processingStart := time.Now()
	defer func() {
		used := time.Since(processingStart)
		syncState := application.Status.SynchronizationState
		if syncState == "" {
			syncState = "unknown"
		}
		metrics.ApplicationProcessingTime.With(prometheus.Labels{
			metrics.LabelSyncState: syncState,
		}).Observe(used.Seconds())
	}()

	logger.Infof("Creating secret(s)")
	if r.Recorder != nil {
		r.Recorder.Eventf(&application, nil, corev1.EventTypeNormal, "CreateSecrets", "CreateSecrets", "Creating Aiven secrets")
	}
	secrets, err := r.Manager.CreateSecret(ctx, &application, logger)
	if err != nil {
		utils.LocalFail("CreateSecret", &application, err, logger)
		if r.Recorder != nil {
			r.Recorder.Eventf(&application, nil, corev1.EventTypeWarning, "SecretGenerationFailed", "CreateSecrets", "Failed generating secrets: %v", err)
		}
		return fail(err)
	}

	logger.Infof("Saving %d secret(s) to cluster", len(secrets))
	for _, secret := range secrets {
		logger := logger.WithFields(log.Fields{"secret_name": secret.Name})
		if err := r.SaveSecret(ctx, &secret, logger); err != nil {
			utils.LocalFail("SaveSecret", &application, err, logger)
			if r.Recorder != nil {
				r.Recorder.Eventf(&application, nil, corev1.EventTypeWarning, "SecretWriteFailed", "SaveSecret", "Failed saving secret %s: %v", secret.Name, err)
			}
			return fail(err)
		}
	}

	success(&application, hash)
	if r.Recorder != nil {
		r.Recorder.Eventf(&application, nil, corev1.EventTypeNormal, "Synchronized", "Sync", "Credentials synchronized and secrets stored")
	}

	return ctrl.Result{}, nil
}

func (r *AivenApplicationReconciler) HandleProtectedAndTimeLimited(ctx context.Context, application aiven_nais_io_v1.AivenApplication, logger log.FieldLogger) (bool, error) {
	if application.Spec.ExpiresAt == nil {
		return false, nil
	}

	parsedTimeStamp, err := utils.Parse(application.FormatExpiresAt())
	if err != nil {
		return false, fmt.Errorf("could not parse timestamp: %s", err)
	}

	if !utils.Expired(parsedTimeStamp) {
		return false, nil
	}

	logger.Infof("Application timelimit exceded: %s", parsedTimeStamp.String())
	if r.Recorder != nil {
		r.Recorder.Eventf(&application, nil, corev1.EventTypeNormal, "Expired", "Expire", "Application expired at %s", parsedTimeStamp.Format(time.RFC3339))
	}
	err = r.DeleteApplication(ctx, application, logger)
	if err != nil {
		return false, err
	}

	return true, nil
}

func (r *AivenApplicationReconciler) DeleteApplication(ctx context.Context, application aiven_nais_io_v1.AivenApplication, logger log.FieldLogger) error {
	err := r.Delete(ctx, &application)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Debugf("application do not exist in cluster: %s", err)
		} else {
			return fmt.Errorf("unable to delete application from cluster: %s", err)
		}
	} else {
		logger.Infof("Application deleted from cluster")
		if r.Recorder != nil {
			r.Recorder.Eventf(&application, nil, corev1.EventTypeNormal, "Deleted", "Delete", "Application resource deleted from cluster")
		}
	}
	return nil
}

func success(application *aiven_nais_io_v1.AivenApplication, hash string) {
	s := &application.Status
	s.SynchronizationHash = hash
	s.SynchronizationState = rolloutComplete
	s.SynchronizedGeneration = application.GetGeneration()
	s.AddCondition(aiven_nais_io_v1.AivenApplicationCondition{
		Type:   aiven_nais_io_v1.AivenApplicationSucceeded,
		Status: corev1.ConditionTrue,
	})
	s.AddCondition(aiven_nais_io_v1.AivenApplicationCondition{
		Type:   aiven_nais_io_v1.AivenApplicationAivenFailure,
		Status: corev1.ConditionFalse,
	})
	s.AddCondition(aiven_nais_io_v1.AivenApplicationCondition{
		Type:   aiven_nais_io_v1.AivenApplicationLocalFailure,
		Status: corev1.ConditionFalse,
	})
}

func (r *AivenApplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	opts := controller.Options{
		MaxConcurrentReconciles: 10,
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiven_nais_io_v1.AivenApplication{}).
		WithOptions(opts).
		WithEventFilter(predicate.Or(
			predicate.GenerationChangedPredicate{},
			predicate.AnnotationChangedPredicate{},
			predicate.LabelChangedPredicate{},
		)).
		Complete(r)
}

func (r *AivenApplicationReconciler) SaveSecret(ctx context.Context, secret *corev1.Secret, logger log.FieldLogger) error {
	key := client.ObjectKey{
		Namespace: secret.Namespace,
		Name:      secret.Name,
	}

	ctx, cancel := context.WithTimeout(ctx, secretWriteTimeout)
	defer cancel()

	old := &corev1.Secret{}
	err := metrics.ObserveKubernetesLatency("Secret_Get", func() error {
		return r.Get(ctx, key, old)
	})

	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Infof("Saving secret")
			err = metrics.ObserveKubernetesLatency("Secret_Create", func() error {
				return r.Create(ctx, secret)
			})
			if r.Recorder != nil {
				if err == nil {
					r.Recorder.Eventf(secret, nil, corev1.EventTypeNormal, "SecretCreated", "CreateSecret", "Secret %s created", secret.Name)
				} else {
					r.Recorder.Eventf(secret, nil, corev1.EventTypeWarning, "SecretCreateFailed", "CreateSecret", "Failed creating secret %s: %v", secret.Name, err)
				}
			}
		}
	} else {
		logger.Infof("Updating secret")
		secret.ResourceVersion = old.ResourceVersion
		err = metrics.ObserveKubernetesLatency("Secret_Update", func() error {
			return r.Update(ctx, secret)
		})
		if r.Recorder != nil {
			if err == nil {
				r.Recorder.Eventf(secret, nil, corev1.EventTypeNormal, "SecretUpdated", "UpdateSecret", "Secret %s updated", secret.Name)
			} else {
				r.Recorder.Eventf(secret, nil, corev1.EventTypeWarning, "SecretUpdateFailed", "UpdateSecret", "Failed updating secret %s: %v", secret.Name, err)
			}
		}
	}

	if err == nil {
		metrics.KubernetesResourcesWritten.With(prometheus.Labels{
			metrics.LabelResourceType: "Secret",
			metrics.LabelNamespace:    secret.GetNamespace(),
		}).Inc()
	}

	metrics.KubernetesResourcesNotWritten.With(prometheus.Labels{
		metrics.LabelResourceType: "Secret",
		metrics.LabelNamespace:    secret.GetNamespace(),
	}).Inc()
	return err
}

func (r *AivenApplicationReconciler) NeedsSynchronization(ctx context.Context, application aiven_nais_io_v1.AivenApplication, hash string, logger log.FieldLogger) (bool, error) {
	if application.Status.SynchronizationHash != hash {
		logger.Infof("Hash changed; needs synchronization")
		metrics.ProcessingReason.WithLabelValues(metrics.HashChanged.String()).Inc()
		if r.Recorder != nil {
			r.Recorder.Eventf(&application, nil, corev1.EventTypeNormal, "HashChanged", "CheckSync", "Spec hash changed; resync needed")
		}
		return true, nil
	}

	var keys []client.ObjectKey
	if application.Spec.Kafka != nil && application.Spec.Kafka.SecretName != "" {
		keys = append(keys, client.ObjectKey{
			Namespace: application.GetNamespace(),
			Name:      application.Spec.Kafka.SecretName,
		})
	}

	if application.Spec.Valkey != nil {
		for _, valkey := range application.Spec.Valkey {
			if valkey.SecretName != "" {
				keys = append(keys, client.ObjectKey{
					Namespace: application.GetNamespace(),
					Name:      valkey.SecretName,
				})
			}
		}
	}

	if application.Spec.OpenSearch != nil && application.Spec.OpenSearch.SecretName != "" {
		keys = append(keys, client.ObjectKey{
			Namespace: application.GetNamespace(),
			Name:      application.Spec.OpenSearch.SecretName,
		})
	}

	for _, k := range keys {
		dst := corev1.Secret{}
		err := r.Get(ctx, k, &dst)
		switch {
		case k8serrors.IsNotFound(err):
			logger.Infof("Secret not found; needs synchronization")
			metrics.ProcessingReason.WithLabelValues(metrics.MissingSecret.String()).Inc()
			if r.Recorder != nil {
				r.Recorder.Eventf(&application, nil, corev1.EventTypeNormal, "MissingSecret", "CheckSync", "Secret %s not found; resync needed", k.Name)
			}
			return true, nil
		case err != nil:
			return false, fmt.Errorf("unable to retrieve secret from cluster: %s", err)
		}

	}
	logger.Infof("Already synchronized")
	if r.Recorder != nil {
		r.Recorder.Eventf(&application, nil, corev1.EventTypeNormal, "UpToDate", "CheckSync", "Credentials already synchronized")
	}

	return false, nil
}
