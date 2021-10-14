package secret

import (
	"fmt"
	appsv1 "k8s.io/api/apps/v1"
	"strconv"
	"time"

	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/utils"
)

const (
	AivenSecretUpdatedKey = "AIVEN_SECRET_UPDATED"
)

type Handler struct {
}

func (s Handler) Apply(application *aiven_nais_io_v1.AivenApplication, rs *appsv1.ReplicaSet, secret *corev1.Secret, logger *log.Entry) error {
	secretName := application.Spec.SecretName

	errors := validation.IsDNS1123Label(secretName)
	hasErrors := len(errors) > 0

	if hasErrors {
		return fmt.Errorf("invalid secret name '%s': %w", secretName, utils.UnrecoverableError)
	}

	secret.ObjectMeta = metav1.ObjectMeta{
		Name:      secretName,
		Namespace: application.GetNamespace(),
		Labels: map[string]string{
			constants.AppLabel:        application.GetName(),
			constants.TeamLabel:       application.GetNamespace(),
			constants.SecretTypeLabel: constants.AivenatorSecretType,
		},
		Annotations: setAnnotations(application),
	}
	secret.StringData = utils.MergeStringMap(secret.StringData, map[string]string{
		AivenSecretUpdatedKey: time.Now().Format(time.RFC3339),
	})

	if rs != nil {
		secret.SetOwnerReferences([]metav1.OwnerReference{{
			APIVersion: rs.APIVersion,
			Kind:       rs.Kind,
			Name:       rs.Name,
			UID:        rs.UID,
		}})
	} else {
		secret.SetOwnerReferences([]metav1.OwnerReference{application.GetOwnerReference()})
	}

	return nil
}

func setAnnotations(application *aiven_nais_io_v1.AivenApplication) map[string]string {
	annotations := map[string]string{
		nais_io_v1.DeploymentCorrelationIDAnnotation: application.GetAnnotations()[nais_io_v1.DeploymentCorrelationIDAnnotation],
		constants.AivenatorProtectedAnnotation:       strconv.FormatBool(application.Spec.Protected),
	}

	if application.Spec.Protected && application.Spec.ExpiresAt != nil {
		annotations[constants.AivenatorProtectedExpireAtAnnotation] = strconv.FormatBool(true)
	}
	return annotations
}

func (s Handler) Cleanup(_ *corev1.Secret, _ *log.Entry) error {
	return nil
}
