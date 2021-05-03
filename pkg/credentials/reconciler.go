package credentials

import (
	"context"
	"fmt"
	kafka_nais_io_v1 "github.com/nais/liberator/pkg/apis/kafka.nais.io/v1"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

const requeueInterval = time.Second * 10

type AivenApplicationReconciler struct {
	client.Client
	Logger  *log.Logger
	Creator Creator
}

func (r *AivenApplicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var application kafka_nais_io_v1.AivenApplication

	logger := log.NewEntry(r.Logger)

	logger = logger.WithFields(log.Fields{
		"aiven_application": req.Name,
		"namespace":         req.Namespace,
	})

	logger.Infof("Processing request")
	defer func() {
		logger.Infof("Finished processing request")
	}()

	fail := func(err error, requeue bool) (ctrl.Result, error) {
		logger.Error(err)
		cr := &ctrl.Result{}
		if requeue {
			cr.RequeueAfter = requeueInterval
		}
		return *cr, nil
	}

	err := r.Get(ctx, req.NamespacedName, &application)
	switch {
	case errors.IsNotFound(err):
		return fail(fmt.Errorf("resource deleted from cluster; noop"), false)
	case err != nil:
		return fail(fmt.Errorf("unable to retrieve resource from cluster: %s", err), true)
	}

	logger = logger.WithFields(log.Fields{
		"secret_name": application.Spec.SecretName,
	})

	logger.Infof("Application exists; processing")
	secret, err := r.Process(ctx, application, logger)
	if err != nil {
		return fail(err, true)
	}
	if secret == nil {
		return ctrl.Result{}, nil
	}

	logger.Infof("Saving secret to cluster")
	err = r.SaveSecret(ctx, secret, logger)
	if err != nil {
		return fail(fmt.Errorf("unable to save secret resource to cluster: %s", err), true)
	}

	return ctrl.Result{}, nil
}

func (r *AivenApplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kafka_nais_io_v1.AivenApplication{}).
		Complete(r)
}

func (r *AivenApplicationReconciler) Process(ctx context.Context, application kafka_nais_io_v1.AivenApplication, logger *log.Entry) (*v1.Secret, error) {
	needsSync, err := r.NeedsSynchronization(ctx, application, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to determine synchronization status: %s", err)
	}

	if needsSync {
		logger.Infof("Needs synchronization; creating secret")
		return r.Creator.CreateSecret(&application)
	}

	return nil, nil
}

func (r *AivenApplicationReconciler) SaveSecret(ctx context.Context, secret *v1.Secret, logger *log.Entry) error {
	return fmt.Errorf("not implemented")
}

func (r *AivenApplicationReconciler) NeedsSynchronization(ctx context.Context, application kafka_nais_io_v1.AivenApplication, logger *log.Entry) (bool, error) {
	hash, err := application.Hash()
	if err != nil {
		return false, fmt.Errorf("unable to calculate synchronization hash: %s", err)
	}

	if application.Status.SynchronizationHash != hash {
		return true, nil
	}

	key := client.ObjectKey{
		Namespace: application.GetNamespace(),
		Name:      application.Spec.SecretName,
	}
	old := v1.Secret{}
	err = r.Get(ctx, key, &old)
	switch {
	case errors.IsNotFound(err):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("unable to retrieve secret from cluster: %s", err)
	}

	return false, nil
}
