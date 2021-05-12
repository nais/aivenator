package secrets

import (
	"context"
	"github.com/nais/aivenator/pkg/handlers/secret"
	"github.com/nais/aivenator/pkg/metrics"
	"github.com/nais/liberator/pkg/kubernetes"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

type Janitor struct {
	client.Client
	Logger *log.Entry
	Ctx    context.Context
}

func (j *Janitor) Start(janitorInterval time.Duration) {
	ticker := time.NewTicker(janitorInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				j.CleanUnusedSecrets()
			}
		}
	}()
}

func (j *Janitor) CleanUnusedSecrets() {
	var secrets corev1.SecretList
	var mLabels = client.MatchingLabels{
		secret.SecretTypeLabel: secret.AivenatorSecretType,
	}

	if err := j.List(j.Ctx, &secrets, mLabels); err != nil {
		switch {
		case errors.IsNotFound(err):
			j.Logger.Info("found no secrets managed by Aivenator")
		case err != nil:
			j.Logger.Errorf("failed to retrieve list of secrets: %s", err)
		}
		return
	}

	podList := corev1.PodList{}
	err := j.List(j.Ctx, &podList)
	if err != nil {
		j.Logger.Errorf("failed to retrieve list of pods: %s", err)
	}

	secretLists := kubernetes.ListUsedAndUnusedSecretsForPods(secrets, podList)
	found := len(secretLists.Unused.Items)
	if found > 0 {
		j.Logger.Infof("Found %d unused secrets managed by Aivenator", found)

		for _, oldSecret := range secretLists.Unused.Items {
			if err := j.Delete(j.Ctx, &oldSecret); err != nil {
				j.Logger.Errorf("failed to delete secret: %s", err)
			} else {
				metrics.KubernetesResourcesDeleted.With(prometheus.Labels{
					metrics.LabelResourceType: oldSecret.GroupVersionKind().String(),
					metrics.LabelNamespace:    oldSecret.GetNamespace(),
				}).Inc()
			}
		}
	}
}
