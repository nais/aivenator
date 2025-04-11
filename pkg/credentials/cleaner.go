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

type Cleaner struct {
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
	Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error
	Scheme() *runtime.Scheme
}

func (j *Cleaner) CleanUnusedSecretsForApplication(ctx context.Context, application aiven_nais_io_v1.AivenApplication) error {
	var secrets corev1.SecretList
	var mLabels = client.MatchingLabels{
		constants.AppLabel:        application.GetName(),
		constants.SecretTypeLabel: constants.AivenatorSecretType,
	}

	err := metrics.ObserveKubernetesLatency("Secret_List", func() error {
		return j.List(ctx, &secrets, mLabels, client.InNamespace(application.GetNamespace()))
	})
	if err != nil {
		return fmt.Errorf("failed to retrieve list of secrets: %v", err)
	}

	objects, err := j.collectPossibleUsers(ctx, application.GetName())
	if err != nil {
		return err
	}

	_, err = j.cleanUnusedSecrets(ctx, secrets, objects)
	return err
}

func (j *Cleaner) CleanUnusedSecrets(ctx context.Context) error {
	var secrets corev1.SecretList
	var mLabels = client.MatchingLabels{
		constants.SecretTypeLabel: constants.AivenatorSecretType,
	}

	err := metrics.ObserveKubernetesLatency("Secret_List", func() error {
		return j.List(ctx, &secrets, mLabels)
	})
	if err != nil {
		return fmt.Errorf("failed to retrieve list of secrets: %v", err)
	}

	objects, err := j.collectPossibleUsers(ctx, "")
	if err != nil {
		return err
	}

	counts, err := j.cleanUnusedSecrets(ctx, secrets, objects)
	if err != nil {
		return err
	}

	metrics.SecretsManaged.With(prometheus.Labels{
		metrics.LabelSecretState: "protected",
	}).Set(float64(counts.Protected))
	metrics.SecretsManaged.With(prometheus.Labels{
		metrics.LabelSecretState: "protected-with-time-limit",
	}).Set(float64(counts.ProtectedWithTimeLimit))
	metrics.SecretsManaged.With(prometheus.Labels{
		metrics.LabelSecretState: "in_use",
	}).Set(float64(counts.InUse))

	return nil
}

func (j *Cleaner) cleanUnusedSecrets(ctx context.Context, secrets corev1.SecretList, objects []client.Object) (*counters, error) {
	podList := corev1.PodList{}
	err := metrics.ObserveKubernetesLatency("Pod_List", func() error {
		return j.List(ctx, &podList)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve list of pods: %v", err)
	}

	secretLists := kubernetes.ListUsedAndUnusedSecretsForPods(secrets, podList)
	counts := counters{
		InUse: len(secretLists.Used.Items),
	}

	if found := len(secretLists.Unused.Items); found > 0 {
		j.Logger.Infof("Found %d secrets managed by Avinator not used by any pods", found)

		for _, oldSecret := range secretLists.Unused.Items {
			err = j.cleanUnusedSecret(ctx, oldSecret, counts, objects)
			if err != nil {
				j.Logger.Warn(err)
			}
		}
	}

	return &counts, nil
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
	case *aiven_nais_io_v1.AivenApplication:
		if t.Spec.SecretName == secretName {
			return true, nil
		} else if t.Spec.Kafka != nil && t.Spec.Kafka.SecretName == secretName {
			return true, nil
		} else if t.Spec.OpenSearch != nil && t.Spec.OpenSearch.SecretName == secretName {
			return true, nil
		} else if t.Spec.Valkey != nil {
			for _, spec := range t.Spec.Valkey {
				if spec.SecretName == secretName {
					return true, nil
				}
			}
		} else {
			return false, nil
		}
	default:
		return false, fmt.Errorf("input object %v is not of supported type", object)
	}

	for _, volume := range volumes {
		if volume.Secret != nil && volume.Secret.SecretName == secretName {
			return true, nil
		}
	}
	return false, nil
}

func (j *Cleaner) cleanUnusedSecret(ctx context.Context, oldSecret corev1.Secret, counts counters, objects []client.Object) error {
	logger := j.Logger.WithFields(log.Fields{
		"secret_name": oldSecret.GetName(),
		"team":        oldSecret.GetNamespace(),
	})

	for _, object := range objects {
		gvk, err := utils.GetGVK(j.Scheme(), object)
		if err != nil {
			logger.Warnf("unable to resolve gvk for %v: %v", object, err)
			continue
		}

		result, err := inUse(object, oldSecret.GetName())
		if err != nil {
			logger.Warn(err)
		}
		if result {
			key := client.ObjectKeyFromObject(object)
			logger.Infof("Secret in use by %v/%v, leaving alone", gvk.Kind, key)
			return nil
		}
	}

	oldSecretAnnotations := oldSecret.GetAnnotations()
	if annotations.HasProtected(oldSecretAnnotations) {
		if annotations.HasTimeLimited(oldSecretAnnotations) {
			expiresAtAnnotation := oldSecretAnnotations[constants.AivenatorProtectedExpiresAtAnnotation]
			if len(expiresAtAnnotation) == 0 {
				logger.Infof("Secret is protected, but doesn't expire; leaving alone")
				return nil
			}

			parsedTimeStamp, err := utils.Parse(expiresAtAnnotation)
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

func (j *Cleaner) collectPossibleUsers(ctx context.Context, appName string) ([]client.Object, error) {
	objects := make([]client.Object, 0)

	aivenAppList := &aiven_nais_io_v1.AivenApplicationList{}
	err := getItemList(ctx, j, aivenAppList, appName)
	if err != nil {
		return nil, fmt.Errorf("failed to list AivenApplications: %v", err)
	}
	for i := range aivenAppList.Items {
		objects = append(objects, &aivenAppList.Items[i])
	}

	replicaSetList := &appsv1.ReplicaSetList{}
	err = getItemList(ctx, j, replicaSetList, appName)
	if err != nil {
		return nil, fmt.Errorf("failed to list ReplicaSets: %v", err)
	}
	for i := range replicaSetList.Items {
		objects = append(objects, &replicaSetList.Items[i])
	}

	cronJobList := &batchv1.CronJobList{}
	err = getItemList(ctx, j, cronJobList, appName)
	if err != nil {
		return nil, fmt.Errorf("failed to list CronJobs: %v", err)
	}
	for i := range cronJobList.Items {
		objects = append(objects, &cronJobList.Items[i])
	}

	JobList := &batchv1.JobList{}
	err = getItemList(ctx, j, JobList, appName)
	if err != nil {
		return nil, fmt.Errorf("failed to list Jobs: %v", err)
	}
	for i := range JobList.Items {
		objects = append(objects, &JobList.Items[i])
	}
	return objects, nil
}

func (j *Cleaner) deleteSecret(ctx context.Context, oldSecret corev1.Secret, logger log.FieldLogger) error {
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

func getItemList(ctx context.Context, c Client, items client.ObjectList, appName string) error {
	var mLabels = client.MatchingLabels{}
	if len(appName) > 0 {
		mLabels[constants.AppLabel] = appName
	}

	err := metrics.ObserveKubernetesLatency("List", func() error {
		return c.List(ctx, items, mLabels)
	})
	if err != nil {
		return fmt.Errorf("failed to retrieve list of resources: %v", err)
	}
	return nil
}
