package credentials

import (
	"context"
	"fmt"
	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/metrics"
	"github.com/nais/liberator/pkg/kubernetes"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Janitor struct {
	Client
	Logger *log.Entry
}

type Client interface {
	List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error
	Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error
	Get(ctx context.Context, key client.ObjectKey, obj client.Object) error
}

func (j *Janitor) CleanUnusedSecrets(ctx context.Context, appName, namespace string) []error {
	var secrets corev1.SecretList
	var mLabels = client.MatchingLabels{
		constants.AppLabel:        appName,
		constants.SecretTypeLabel: constants.AivenatorSecretType,
	}

	if err := j.List(ctx, &secrets, mLabels, client.InNamespace(namespace)); err != nil {
		return []error{fmt.Errorf("failed to retrieve list of secrets: %s", err)}
	}

	podList := corev1.PodList{}
	if err := j.List(ctx, &podList); err != nil {
		return []error{fmt.Errorf("failed to retrieve list of pods: %s", err)}
	}

	errs := make([]error, 0)
	secretLists := kubernetes.ListUsedAndUnusedSecretsForPods(secrets, podList)
	if found := len(secretLists.Unused.Items); found > 0 {
		j.Logger.Debugf("Found %d unused secrets managed by Aivenator", found)

		for _, oldSecret := range secretLists.Unused.Items {
			logger := j.Logger.WithFields(log.Fields{
				"secret_name": oldSecret.GetName(),
				"namespace":   oldSecret.GetNamespace(),
			})
			if protected, ok := oldSecret.GetAnnotations()[constants.AivenatorProtectedAnnotation]; !ok || protected != "true" {
				logger.Debugf("Deleting secret")
				if err := j.Delete(ctx, &oldSecret); err != nil && !errors.IsNotFound(err) {
					err = fmt.Errorf("failed to delete secret %s in namespace %s: %s", oldSecret.GetName(), oldSecret.GetNamespace(), err)
					errs = append(errs, err)
				} else {
					metrics.KubernetesResourcesDeleted.With(prometheus.Labels{
						metrics.LabelResourceType: oldSecret.GroupVersionKind().String(),
						metrics.LabelNamespace:    oldSecret.GetNamespace(),
					}).Inc()
				}
			} else {
				logger.Debugf("Secret is protected, leaving alone")
			}
		}
	}
	return errs
}
