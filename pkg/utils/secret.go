package utils

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/aiven/project"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	AivenSecretUpdatedKey = "AIVEN_SECRET_UPDATED"
	AivenCAKey            = "AIVEN_CA"
)

type SecretConfig struct {
	Project     project.ProjectManager
	ProjectName string
}

func NewSecretConfig(aiven *aiven.Client, projectName string) SecretConfig {
	return SecretConfig{
		Project:     project.NewManager(aiven.CA),
		ProjectName: projectName,
	}
}

func (s SecretConfig) ApplyIndividualSecret(ctx context.Context, application *aiven_nais_io_v1.AivenApplication, secret *corev1.Secret, logger log.FieldLogger) ([]corev1.Secret, error) {
	errors := validation.IsDNS1123Label(secret.Name)
	if len(errors) > 0 {
		return nil, fmt.Errorf("invalid secret name '%s': %w: %v", secret.Name, ErrUnrecoverable, errors)
	}

	updateObjectMeta(application, &secret.ObjectMeta)

	projectCa, err := s.Project.GetCA(ctx, s.ProjectName)
	if err != nil {
		return nil, fmt.Errorf("unable to get project CA: %w", err)
	}

	secret.StringData = MergeStringMap(secret.StringData, map[string]string{
		AivenSecretUpdatedKey: time.Now().Format(time.RFC3339),
		AivenCAKey:            projectCa,
	})
	logger.Infof("Applied individual secret: %s", secret.Name)

	return nil, nil
}

func updateObjectMeta(application *aiven_nais_io_v1.AivenApplication, objMeta *metav1.ObjectMeta) {
	generation := 0
	if v, ok := application.Labels[constants.GenerationLabel]; ok {
		g, err := strconv.Atoi(v)
		if err == nil {
			generation = g
		}
	}

	objMeta.Labels = MergeStringMap(objMeta.Labels, map[string]string{
		constants.AppLabel:              application.GetName(),
		constants.TeamLabel:             application.GetNamespace(),
		constants.SecretTypeLabel:       constants.AivenatorSecretType,
		constants.GenerationLabel:       strconv.Itoa(generation),
		constants.AivenatorProtectedKey: strconv.FormatBool(application.Spec.Protected),
	})
	objMeta.Annotations = MergeStringMap(objMeta.Annotations, createAnnotations(application))
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
