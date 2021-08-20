package secret

import (
	"fmt"
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

func (s Handler) Apply(application *aiven_nais_io_v1.AivenApplication, secret *corev1.Secret, _ *log.Entry) error {
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
		Annotations: map[string]string{
			nais_io_v1.DeploymentCorrelationIDAnnotation: application.GetAnnotations()[nais_io_v1.DeploymentCorrelationIDAnnotation],
			constants.AivenatorProtectedAnnotation:       strconv.FormatBool(application.Spec.Protected),
		},
	}
	secret.StringData = utils.MergeStringMap(secret.StringData, map[string]string{
		AivenSecretUpdatedKey: time.Now().Format(time.RFC3339),
	})

	secret.SetOwnerReferences([]metav1.OwnerReference{application.GetOwnerReference()})

	return nil
}

func (s Handler) Cleanup(_ *corev1.Secret, _ *log.Entry) error {
	return nil
}
