package credentials

import (
	"context"
	"fmt"
	kafka_nais_io_v1 "github.com/nais/liberator/pkg/apis/kafka.nais.io/v1"
	log "github.com/sirupsen/logrus"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type AivenApplicationReconciler struct {
	client.Client
	Logger  *log.Logger
	Creator Creator
}

func (r *AivenApplicationReconciler) Reconcile(ctx context.Context, result ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, fmt.Errorf("not implemented")
}

func (r *AivenApplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kafka_nais_io_v1.AivenApplication{}).
		Complete(r)
}
