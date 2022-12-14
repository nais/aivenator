package credentials

import (
	"context"
	"fmt"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/runtime"

	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	"github.com/nais/liberator/pkg/kubernetes"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/annotations"
	"github.com/nais/aivenator/pkg/metrics"
	"github.com/nais/aivenator/pkg/utils"
)

type Janitor struct {
	Client
	Logger *log.Entry
}

type counters struct {
	Protected              int
	ProtectedWithTimeLimit int
	InUse                  int
}

type Client interface {
	List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error
	Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error
	Get(ctx context.Context, key client.ObjectKey, obj client.Object) error
	Scheme() *runtime.Scheme
}

func (j *Janitor) CleanUnusedSecrets(ctx context.Context, application aiven_nais_io_v1.AivenApplication) []error {
	var secrets corev1.SecretList
	var mLabels = client.MatchingLabels{
		constants.AppLabel:        application.GetName(),
		constants.SecretTypeLabel: constants.AivenatorSecretType,
	}

	err := metrics.ObserveKubernetesLatency("Secret_List", func() error {
		return j.List(ctx, &secrets, mLabels, client.InNamespace(application.GetNamespace()))
	})
	if err != nil {
		return []error{fmt.Errorf("failed to retrieve list of secrets: %s", err)}
	}

	podList := corev1.PodList{}
	err = metrics.ObserveKubernetesLatency("Pod_List", func() error {
		return j.List(ctx, &podList)
	})
	if err != nil {
		return []error{fmt.Errorf("failed to retrieve list of pods: %s", err)}
	}

	errs := make([]error, 0)
	secretLists := kubernetes.ListUsedAndUnusedSecretsForPods(secrets, podList)
	counts := counters{
		InUse: len(secretLists.Used.Items),
	}

	// This list should probably be kept in sync with the one in controllers/aiven_application/reconciler.go
	types := []client.Object{
		&appsv1.ReplicaSet{},
		&batchv1.CronJob{},
		&batchv1.Job{},
	}

	if found := len(secretLists.Unused.Items); found > 0 {
		j.Logger.Infof("Found %d unused secrets managed by Aivenator", found)

		for _, oldSecret := range secretLists.Unused.Items {
			err = j.cleanUnusedSecret(ctx, application, oldSecret, counts, types)
			if err != nil {
				errs = append(errs, err)
			}
		}
	}

	metrics.SecretsManaged.With(prometheus.Labels{
		metrics.LabelNamespace:   application.GetNamespace(),
		metrics.LabelSecretState: "protected",
	}).Set(float64(counts.Protected))
	metrics.SecretsManaged.With(prometheus.Labels{
		metrics.LabelNamespace:   application.GetNamespace(),
		metrics.LabelSecretState: "protected-with-time-limit",
	}).Set(float64(counts.ProtectedWithTimeLimit))
	metrics.SecretsManaged.With(prometheus.Labels{
		metrics.LabelNamespace:   application.GetNamespace(),
		metrics.LabelSecretState: "in_use",
	}).Set(float64(counts.InUse))

	return errs
}

func inUse(object client.Object, secretName string) (bool, error) {
	var volumes []corev1.Volume
	switch t := object.(type) {
	case *appsv1.ReplicaSet:
		volumes = t.Spec.Template.Spec.Volumes
	case *batchv1.Job:
		volumes = t.Spec.Template.Spec.Volumes
	case *batchv1.CronJob:
		volumes = t.Spec.JobTemplate.Spec.Template.Spec.Volumes
	default:
		return false, fmt.Errorf("input object is not of supported type")
	}

	for _, volume := range volumes {
		if volume.Secret != nil && volume.Secret.SecretName == secretName {
			return true, nil
		}
	}
	return false, nil
}

func (j *Janitor) cleanUnusedSecret(ctx context.Context, application aiven_nais_io_v1.AivenApplication, oldSecret corev1.Secret, counts counters, types []client.Object) error {
	logger := j.Logger.WithFields(log.Fields{
		"secret_name": oldSecret.GetName(),
		"namespace":   oldSecret.GetNamespace(),
	})

	if oldSecret.GetName() == application.Spec.SecretName {
		logger.Infof("Will not delete currently requested secret")
		counts.InUse += 1
		return nil
	}

	for _, ownerRef := range oldSecret.GetOwnerReferences() {
		for _, t := range types {
			gvk, err := utils.GetGVK(j.Scheme(), t)
			if err != nil {
				logger.Warnf("unable to resolve gvk for %v: %v", t, err)
				continue
			}
			if ownerRef.Kind == gvk.Kind {
				key := client.ObjectKey{
					Namespace: oldSecret.GetNamespace(),
					Name:      ownerRef.Name,
				}
				err = j.Get(ctx, key, t)
				if err != nil {
					logger.Warnf("unable to get owning object %v from cluster: %v", key, err)
					continue
				}
				result, err := inUse(t, oldSecret.GetName())
				if err != nil {
					logger.Warn(err)
				}
				if result {
					logger.Infof("Secret owned by %v/%v, leaving alone", gvk.Kind, key)
					return nil
				}
			}
		}
	}

	oldSecretAnnotations := oldSecret.GetAnnotations()
	if annotations.HasProtected(oldSecretAnnotations) {
		if annotations.HasTimeLimited(oldSecretAnnotations) {
			if application.Spec.ExpiresAt == nil {
				logger.Infof("Secret is protected, but doesn't expire; leaving alone")
				return nil
			}

			parsedTimeStamp, err := utils.Parse(application.FormatExpiresAt())
			if err != nil {
				counts.ProtectedWithTimeLimit += 1
				logger.Infof("Secret is protected and unable to parse expiresAt, leaving alone")
				return nil
			}

			if utils.Expired(parsedTimeStamp) {
				logger.Infof("Protected, but expired secret, deleting")
				return j.deleteSecret(ctx, oldSecret, logger)
			} else {
				counts.ProtectedWithTimeLimit += 1
				logger.Infof("Secret is protected and not expired, leaving alone")
				return nil
			}
		} else {
			counts.Protected += 1
			logger.Infof("Secret is protected, leaving alone")
			return nil
		}
	}

	logger.Infof("Secret is not in use, not protected, not owned by ReplicaSet and not currently requested, deleting")
	return j.deleteSecret(ctx, oldSecret, logger)
}

func (j *Janitor) deleteSecret(ctx context.Context, oldSecret corev1.Secret, logger log.FieldLogger) error {
	logger.Infof("Deleting secret")
	err := metrics.ObserveKubernetesLatency("Secret_Delete", func() error {
		return j.Delete(ctx, &oldSecret)
	})
	if err != nil && !errors.IsNotFound(err) {
		err = fmt.Errorf("failed to delete secret %s in namespace %s: %s", oldSecret.GetName(), oldSecret.GetNamespace(), err)
		return err
	} else {
		metrics.KubernetesResourcesDeleted.With(prometheus.Labels{
			metrics.LabelResourceType: "Secret",
			metrics.LabelNamespace:    oldSecret.GetNamespace(),
		}).Inc()
	}
	return nil
}
