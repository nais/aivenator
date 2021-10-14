package credentials

import (
	"context"
	"fmt"
	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/annotations"
	"github.com/nais/aivenator/pkg/metrics"
	"github.com/nais/aivenator/pkg/utils"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
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

func (j *Janitor) CleanUnusedSecrets(ctx context.Context, application aiven_nais_io_v1.AivenApplication) []error {
	var secrets corev1.SecretList
	var mLabels = client.MatchingLabels{
		constants.AppLabel:        application.GetName(),
		constants.SecretTypeLabel: constants.AivenatorSecretType,
	}

	if err := j.List(ctx, &secrets, mLabels, client.InNamespace(application.GetNamespace())); err != nil {
		return []error{fmt.Errorf("failed to retrieve list of secrets: %s", err)}
	}

	podList := corev1.PodList{}
	if err := j.List(ctx, &podList); err != nil {
		return []error{fmt.Errorf("failed to retrieve list of pods: %s", err)}
	}

	errs := make([]error, 0)
	secretLists := kubernetes.ListUsedAndUnusedSecretsForPods(secrets, podList)
	counters := struct {
		Protected              int
		ProtectedWithTimeLimit int
		InUse                  int
	}{
		InUse: len(secretLists.Used.Items),
	}

	if found := len(secretLists.Unused.Items); found > 0 {
		j.Logger.Debugf("Found %d unused secrets managed by Aivenator", found)

		for _, oldSecret := range secretLists.Unused.Items {
			logger := j.Logger.WithFields(log.Fields{
				"secret_name": oldSecret.GetName(),
				"namespace":   oldSecret.GetNamespace(),
			})

			if oldSecret.GetName() == application.Spec.SecretName {
				logger.Debugf("Will not delete currently requested secret")
				counters.InUse += 1
				continue
			}

			for _, ownerRef := range oldSecret.GetOwnerReferences() {
				if ownerRef.Kind == "ReplicaSet" {
					logger.Debugf("Secret owned by ReplicaSet, leaving alone")
					continue
				}
			}

			oldSecretAnnotations := oldSecret.GetAnnotations()
			if annotations.HasProtected(oldSecretAnnotations) {
				if annotations.HasTimeLimited(oldSecretAnnotations) {
					parsedTimeStamp := utils.ParseTimestamp(application.FormatExpiresAt(), &errs)
					if utils.Expired(parsedTimeStamp) {
						j.deleteSecret(ctx, oldSecret, &errs)
					} else {
						counters.ProtectedWithTimeLimit += 1
						logger.Debugf("Secret is protected and not expired, leaving alone")
					}
				} else {
					counters.Protected += 1
					logger.Debugf("Secret is protected, leaving alone")
				}
			} else {
				j.deleteSecret(ctx, oldSecret, &errs)
			}
		}
	}

	metrics.SecretsManaged.With(prometheus.Labels{
		metrics.LabelNamespace:   application.GetNamespace(),
		metrics.LabelSecretState: "protected",
	}).Set(float64(counters.Protected))
	metrics.SecretsManaged.With(prometheus.Labels{
		metrics.LabelNamespace:   application.GetNamespace(),
		metrics.LabelSecretState: "protected-with-time-limit",
	}).Set(float64(counters.ProtectedWithTimeLimit))
	metrics.SecretsManaged.With(prometheus.Labels{
		metrics.LabelNamespace:   application.GetNamespace(),
		metrics.LabelSecretState: "in_use",
	}).Set(float64(counters.InUse))

	return errs
}

func (j *Janitor) deleteSecret(ctx context.Context, oldSecret corev1.Secret, errs *[]error) {
	j.Logger.Debugf("Deleting secret")
	if err := j.Delete(ctx, &oldSecret); err != nil && !errors.IsNotFound(err) {
		err = fmt.Errorf("failed to delete secret %s in namespace %s: %s", oldSecret.GetName(), oldSecret.GetNamespace(), err)
		*errs = append(*errs, err)
	} else {
		metrics.KubernetesResourcesDeleted.With(prometheus.Labels{
			metrics.LabelResourceType: "Secret",
			metrics.LabelNamespace:    oldSecret.GetNamespace(),
		}).Inc()
	}
}
