package secrets

import (
	"context"
	"fmt"
	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/credentials"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"time"
)

const (
	requeueInterval = time.Second * 10
)

type SecretsFinalizer struct {
	client.Client
	Logger  *log.Entry
	Manager credentials.Manager
}

func (s *SecretsFinalizer) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var secret v1.Secret

	logger := s.Logger.WithFields(log.Fields{
		"secret_name": req.Name,
		"namespace":   req.Namespace,
	})

	failRetry := func(err error) (ctrl.Result, error) {
		if err != nil {
			logger.Error(err)
		}
		cr := ctrl.Result{}
		cr.RequeueAfter = requeueInterval
		return cr, nil
	}

	err := s.Get(ctx, req.NamespacedName, &secret)
	switch {
	case errors.IsNotFound(err):
		logger.Info("resource deleted from cluster; noop")
		return ctrl.Result{}, nil
	case err != nil:
		return failRetry(fmt.Errorf("unable to retrieve resource from cluster: %s", err))
	}

	logger.Info("Secret will be deleted, cleaning up external resources")
	err = s.Manager.Cleanup(ctx, &secret, logger)
	if err != nil {
		return failRetry(fmt.Errorf("unable to clean up external resources: %s", err))
	}

	controllerutil.RemoveFinalizer(&secret, constants.AivenatorFinalizer)

	err = s.Update(ctx, &secret)
	if err != nil {
		return failRetry(fmt.Errorf("failed to save updated secret: %s", err))
	}

	return ctrl.Result{}, nil
}

func (s *SecretsFinalizer) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Secret{}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(createEvent event.CreateEvent) bool {
				return false // We don't care about secrets created, as the application reconciler is responsible for adding finalizer
			},
			DeleteFunc: func(event event.DeleteEvent) bool {
				return false // We've done our cleanup before the Delete event happens
			},
			UpdateFunc: func(updateEvent event.UpdateEvent) bool {
				return controllerutil.ContainsFinalizer(updateEvent.ObjectNew, constants.AivenatorFinalizer) &&
					!updateEvent.ObjectNew.GetDeletionTimestamp().IsZero()
			},
			GenericFunc: func(genericEvent event.GenericEvent) bool {
				return false // We probably don't care about this either #yolo
			},
		}).
		Complete(s)
}
