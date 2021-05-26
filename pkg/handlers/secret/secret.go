package secret

import (
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	nais_io_v1alpha1 "github.com/nais/liberator/pkg/apis/nais.io/v1alpha1"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strconv"
)

const (
	AppLabel                     = "app"
	TeamLabel                    = "team"
	SecretTypeLabel              = "type"
	AivenatorSecretType          = "aivenator.aiven.nais.io"
	AivenatorProtectedAnnotation = "aivenator.aiven.nais.io/protected"
)

type Handler struct {
}

func (s Handler) Apply(application *aiven_nais_io_v1.AivenApplication, secret *corev1.Secret, _ *log.Entry) error {
	secret.ObjectMeta = metav1.ObjectMeta{
		Name:      application.Spec.SecretName,
		Namespace: application.GetNamespace(),
		Labels: map[string]string{
			AppLabel:        application.GetName(),
			TeamLabel:       application.GetNamespace(),
			SecretTypeLabel: AivenatorSecretType,
		},
		Annotations: map[string]string{
			nais_io_v1alpha1.DeploymentCorrelationIDAnnotation: application.GetAnnotations()[nais_io_v1alpha1.DeploymentCorrelationIDAnnotation],
			AivenatorProtectedAnnotation:                       strconv.FormatBool(application.Spec.Protected),
		},
	}

	secret.SetOwnerReferences([]metav1.OwnerReference{application.GetOwnerReference()})

	return nil
}

func (s Handler) Cleanup(_ *corev1.Secret, _ *log.Entry) error {
	return nil
}
