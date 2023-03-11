package secret

import (
	"fmt"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func (s Handler) Apply(application *aiven_nais_io_v1.AivenApplication, objects []client.Object, secret *corev1.Secret, logger *log.Entry) error {
	secretName := application.Spec.SecretName

	errors := validation.IsDNS1123Label(secretName)
	hasErrors := len(errors) > 0

	if hasErrors {
		return fmt.Errorf("invalid secret name '%s': %w", secretName, utils.UnrecoverableError)
	}

	updateObjectMeta(application, &secret.ObjectMeta)
	retries := utils.GetSecretRetries(secret)
	utils.SetSecretRetries(secret, retries+1)

	secret.StringData = utils.MergeStringMap(secret.StringData, map[string]string{
		AivenSecretUpdatedKey: time.Now().Format(time.RFC3339),
	})

	return updateOwnerReferences(application, objects, secret)
}

func updateOwnerReferences(application *aiven_nais_io_v1.AivenApplication, objects []client.Object, secret *corev1.Secret) error {
	appOwnerReference := application.GetOwnerReference()

	ownerReferences := make(map[metav1.OwnerReference]bool, len(secret.GetOwnerReferences()))
	for _, reference := range secret.GetOwnerReferences() {
		ownerReferences[reference] = true
	}
	ownerReferences[appOwnerReference] = true

	for _, object := range objects {
		ownerReference, err := utils.MakeOwnerReference(object)
		if err != nil {
			return err
		}
		ownerReferences[ownerReference] = true
		ownerReferences[appOwnerReference] = false
	}

	var newOwnerReferences []metav1.OwnerReference
	for reference, keep := range ownerReferences {
		if keep {
			newOwnerReferences = append(newOwnerReferences, reference)
		}
	}

	secret.ObjectMeta.OwnerReferences = newOwnerReferences
	return nil
}

func updateObjectMeta(application *aiven_nais_io_v1.AivenApplication, objMeta *metav1.ObjectMeta) {
	objMeta.Name = application.Spec.SecretName
	objMeta.Namespace = application.GetNamespace()
	objMeta.Labels = utils.MergeStringMap(objMeta.Labels, map[string]string{
		constants.AppLabel:        application.GetName(),
		constants.TeamLabel:       application.GetNamespace(),
		constants.SecretTypeLabel: constants.AivenatorSecretType,
	})
	objMeta.Annotations = utils.MergeStringMap(objMeta.Annotations, createAnnotations(application))
}

func createAnnotations(application *aiven_nais_io_v1.AivenApplication) map[string]string {
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
