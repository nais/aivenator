package secrets

import (
	"context"
	"github.com/nais/aivenator/pkg/handlers/secret"
	"github.com/nais/liberator/pkg/kubernetes"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func CleanUnusedSecrets(ctx context.Context, c client.Client, logger *log.Entry) {
	var secrets corev1.SecretList
	var mLabels = client.MatchingLabels{
		secret.SecretTypeLabel: secret.AivenatorSecretType,
	}

	if err := c.List(ctx, &secrets, mLabels); err != nil {
		switch {
		case errors.IsNotFound(err):
			logger.Info("found no secrets managed by Aivenator")
		case err != nil:
			logger.Errorf("failed to retrieve list of secrets: %s", err)
		}
		return
	}

	podList := corev1.PodList{}
	err := c.List(ctx, &podList)
	if err != nil {
		logger.Errorf("failed to retrieve list of pods: %s", err)
	}

	secretLists := kubernetes.ListUsedAndUnusedSecretsForPods(secrets, podList)
	found := len(secretLists.Unused.Items)
	if found > 0 {
		logger.Infof("Found %d unused secrets managed by Aivenator", found)

		for _, oldSecret := range secretLists.Unused.Items {
			if err := c.Delete(ctx, &oldSecret); err != nil {
				logger.Errorf("failed to delete secret: %s", err)
			}
		}
	}
}
