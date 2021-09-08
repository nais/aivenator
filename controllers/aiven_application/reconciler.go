package aiven_application

import (
	"context"
	"errors"
	"fmt"
	"time"

	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/nais/aivenator/pkg/credentials"
	"github.com/nais/aivenator/pkg/metrics"
	"github.com/nais/aivenator/pkg/utils"
)

const (
	requeueInterval    = time.Second * 10
	secretWriteTimeout = time.Second * 2

	rolloutComplete = "RolloutComplete"
	rolloutFailed   = "RolloutFailed"
)

func NewReconciler(mgr manager.Manager, logger *log.Logger, credentialsManager credentials.Manager, credentialsJanitor credentials.Janitor) AivenApplicationReconciler {
	return AivenApplicationReconciler{
		Client:  mgr.GetClient(),
		Logger:  logger.WithFields(log.Fields{"component": "AivenApplicationReconciler"}),
		Manager: credentialsManager,
		Janitor: credentialsJanitor,
	}
}

type AivenApplicationReconciler struct {
	client.Client
	Logger  *log.Entry
	Manager credentials.Manager
	Janitor credentials.Janitor
}

func (r *AivenApplicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var application aiven_nais_io_v1.AivenApplication

	logger := r.Logger.WithFields(log.Fields{
		"aiven_application": req.Name,
		"namespace":         req.Namespace,
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

		if !errors.Is(err, utils.UnrecoverableError) {
			cr.RequeueAfter = requeueInterval
		}

		return cr, nil
	}

	err := r.Get(ctx, req.NamespacedName, &application)
	switch {
	case k8serrors.IsNotFound(err):
		return fail(fmt.Errorf("resource deleted from cluster; noop: %w", utils.UnrecoverableError))
	case err != nil:
		return fail(fmt.Errorf("unable to retrieve resource from cluster: %s", err))
	}

	logger = logger.WithFields(log.Fields{
		"secret_name": application.Spec.SecretName,
	})

	logger.Infof("Application exists; processing")
	defer func() {
		application.Status.SynchronizationTime = &v1.Time{time.Now()}
		application.Status.ObservedGeneration = application.GetGeneration()
		err := r.Status().Update(ctx, &application)
		if err != nil {
			logger.Errorf("Unable to update status of application: %s\nWanted to save status: %+v", err, application.Status)
		} else {
			metrics.KubernetesResourcesWritten.With(prometheus.Labels{
				metrics.LabelResourceType: "AivenApplication",
				metrics.LabelNamespace:    application.GetNamespace(),
			}).Inc()
		}
	}()

	hash, err := application.Hash()
	if err != nil {
		utils.LocalFail("Hash", &application, err, logger)
		return fail(err)
	}

	applicationDeleted, err := r.HandleProtectedAndTimeLimited(ctx, application, logger)
	if err != nil {
		utils.LocalFail("HandleProtectedAndTimeLimited", &application, err, logger)
		return fail(err)
	}

	if applicationDeleted {
		return ctrl.Result{}, nil
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
		used := time.Now().Sub(processingStart)
		syncState := application.Status.SynchronizationState
		if syncState == "" {
			syncState = "unknown"
		}
		metrics.ApplicationProcessingTime.With(prometheus.Labels{
			metrics.LabelSyncState: syncState,
		}).Observe(used.Seconds())
	}()

	logger.Infof("Creating secret")
	secret, err := r.Manager.CreateSecret(&application, logger)
	if err != nil {
		utils.LocalFail("CreateSecret", &application, err, logger)
		return fail(err)
	}

	logger.Infof("Saving secret to cluster")
	err = r.SaveSecret(ctx, secret, logger)
	if err != nil {
		utils.LocalFail("SaveSecret", &application, err, logger)
		return fail(err)
	}

	success(&application, hash)

	errs := r.Janitor.CleanUnusedSecrets(ctx, application)
	if len(errs) > 0 {
		for _, err := range errs {
			logger.Error(err)
		}
	}

	return ctrl.Result{}, nil
}

func (r *AivenApplicationReconciler) HandleProtectedAndTimeLimited(ctx context.Context, application aiven_nais_io_v1.AivenApplication, logger *log.Entry) (bool, error) {
	if application.Spec.ExpiresAt == "" {
		return false, nil
	}

	parsedTimeStamp, err := utils.Parse(application.Spec.ExpiresAt)
	if err != nil {
		return false, fmt.Errorf("could not parse timestamp: %s", parsedTimeStamp.String())
	}

	if utils.Expired(parsedTimeStamp) {
		log.Infof("Application timelimit exceded: %s", parsedTimeStamp.String())
		err := r.DeleteApplication(ctx, application, logger)
		if err != nil {
			return false, err
		}
	} else {
		return false, nil
	}
	return true, nil
}

func (r *AivenApplicationReconciler) DeleteApplication(ctx context.Context, application aiven_nais_io_v1.AivenApplication, logger *log.Entry) error {
	err := r.Delete(ctx, &application)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Debugf("application do not exist in cluster: %s", err)
		} else {
			return fmt.Errorf("unable to delete application from cluster: %s", err)
		}
	} else {
		logger.Infof("Application deleted from cluster")
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
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiven_nais_io_v1.AivenApplication{}).
		WithEventFilter(predicate.Or(
			predicate.GenerationChangedPredicate{},
			predicate.AnnotationChangedPredicate{},
			predicate.LabelChangedPredicate{},
		)).
		Complete(r)
}

func (r *AivenApplicationReconciler) SaveSecret(ctx context.Context, secret *corev1.Secret, logger *log.Entry) error {
	key := client.ObjectKey{
		Namespace: secret.Namespace,
		Name:      secret.Name,
	}

	ctx, cancel := context.WithTimeout(ctx, secretWriteTimeout)
	defer cancel()

	old := &corev1.Secret{}
	err := r.Get(ctx, key, old)

	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Infof("Saving secret")
			err = r.Create(ctx, secret)
		}
	} else {
		logger.Infof("Updating secret")
		secret.ResourceVersion = old.ResourceVersion
		err = r.Update(ctx, secret)
	}

	if err == nil {
		metrics.KubernetesResourcesWritten.With(prometheus.Labels{
			metrics.LabelResourceType: "Secret",
			metrics.LabelNamespace:    secret.GetNamespace(),
		}).Inc()
	}

	return err
}

func (r *AivenApplicationReconciler) NeedsSynchronization(ctx context.Context, application aiven_nais_io_v1.AivenApplication, hash string, logger *log.Entry) (bool, error) {
	if application.Status.SynchronizationHash != hash {
		logger.Infof("Hash changed; needs synchronization")
		return true, nil
	}

	key := client.ObjectKey{
		Namespace: application.GetNamespace(),
		Name:      application.Spec.SecretName,
	}
	old := corev1.Secret{}
	err := r.Get(ctx, key, &old)
	switch {
	case k8serrors.IsNotFound(err):
		logger.Infof("Secret not found; needs synchronization")
		return true, nil
	case err != nil:
		return false, fmt.Errorf("unable to retrieve secret from cluster: %s", err)
	}

	logger.Infof("Already synchronized")
	return false, nil
}
