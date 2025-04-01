package secret

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/pkg/aiven/project"

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
	AivenCAKey            = "AIVEN_CA"
)

type Handler struct {
	project     project.ProjectManager
	projectName string
}

func NewHandler(aiven *aiven.Client, projectName string) Handler {
	return Handler{
		project:     project.NewManager(aiven.CA),
		projectName: projectName,
	}
}

func (s Handler) Apply(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, secret *corev1.Secret, logger log.FieldLogger) ([]*corev1.Secret, error) {
	err := NormalizeSecret(ctx, s.project, s.projectName, application, secret, logger)
	if err != nil {
		return nil, fmt.Errorf("unable to normalize secret: %w", err)
	}

	return []*corev1.Secret{secret}, nil
}

func NormalizeSecret(ctx context.Context, project project.ProjectManager, projectName string, application *aiven_nais_io_v1.AivenApplication, secret *corev1.Secret, logger log.FieldLogger) error {
	secretName := application.Spec.SecretName

	errors := validation.IsDNS1123Label(secretName)
	hasErrors := len(errors) > 0

	if hasErrors {
		return fmt.Errorf("invalid secret name '%s': %w", secretName, utils.ErrUnrecoverable)
	}

	updateObjectMeta(application, &secret.ObjectMeta)

	projectCa, err := project.GetCA(ctx, projectName)
	if err != nil {
		return fmt.Errorf("unable to get project CA: %w", err)
	}

	secret.StringData = utils.MergeStringMap(secret.StringData, map[string]string{
		AivenSecretUpdatedKey: time.Now().Format(time.RFC3339),
		AivenCAKey:            projectCa,
	})

	return nil
}

func updateObjectMeta(application *aiven_nais_io_v1.AivenApplication, objMeta *metav1.ObjectMeta) {
	objMeta.Name = application.Spec.SecretName
	objMeta.Namespace = application.GetNamespace()

	generation := 0
	if v, ok := application.Labels[constants.GenerationLabel]; ok {
		g, err := strconv.Atoi(v)
		if err == nil {
			generation = g
		}
	}

	objMeta.Labels = utils.MergeStringMap(objMeta.Labels, map[string]string{
		constants.AppLabel:              application.GetName(),
		constants.TeamLabel:             application.GetNamespace(),
		constants.SecretTypeLabel:       constants.AivenatorSecretType,
		constants.GenerationLabel:       strconv.Itoa(generation),
		constants.AivenatorProtectedKey: strconv.FormatBool(application.Spec.Protected),
	})
	objMeta.Annotations = utils.MergeStringMap(objMeta.Annotations, createAnnotations(application))
}

func createAnnotations(application *aiven_nais_io_v1.AivenApplication) map[string]string {
	annotations := map[string]string{
		nais_io_v1.DeploymentCorrelationIDAnnotation: application.GetAnnotations()[nais_io_v1.DeploymentCorrelationIDAnnotation],
		constants.AivenatorProtectedKey:              strconv.FormatBool(application.Spec.Protected),
	}

	if application.Spec.Protected && application.Spec.ExpiresAt != nil {
		annotations[constants.AivenatorProtectedWithTimeLimitAnnotation] = strconv.FormatBool(true)
		annotations[constants.AivenatorProtectedExpiresAtAnnotation] = application.Spec.ExpiresAt.Format(time.RFC3339)
	}
	return annotations
}

func (s Handler) Cleanup(ctx context.Context, secret *corev1.Secret, logger log.FieldLogger) error {
	return nil
}
