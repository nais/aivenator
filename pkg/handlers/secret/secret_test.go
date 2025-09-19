package secret

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/aiven/project"
	"github.com/nais/aivenator/pkg/utils"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

const (
	namespace        = "ns"
	applicationName  = "app"
	secretName       = "my-secret"
	projectName      = "test-project"
	correlationId    = "correlation-id"
	projectCA        = "==== PROJECT CA ===="
	secretGeneration = "123"
)

func TestSecret(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Secret Suite")
}

var _ = Describe("secret.Handler ApplyIndividualSecret", func() {
	var handler Handler
	var mockProjects *project.MockProjectManager
	var ctx context.Context
	var cancel context.CancelFunc
	var logger *logrus.Logger

	BeforeEach(func() {
		mockProjects = project.NewMockProjectManager(GinkgoT())
		mockProjects.On("GetCA", mock.Anything, projectName).Return(projectCA, nil).Maybe()
		handler = Handler{Project: mockProjects, ProjectName: projectName}
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		logger = logrus.New()
		logger.Out = GinkgoWriter
	})

	AfterEach(func() { cancel() })

	It("applies labels and namespace for a basic application", func() {
		application := aiven_nais_io_v1.NewAivenApplicationBuilder(applicationName, namespace).
			WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName}).
			Build()
		secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace}}

		_, err := handler.ApplyIndividualSecret(ctx, &application, &secret, logger)
		Expect(err).To(Succeed())

		Expect(secret.Labels[constants.SecretTypeLabel]).To(Equal(constants.AivenatorSecretType))
		Expect(secret.Labels[constants.AppLabel]).To(Equal(application.GetName()))
		Expect(secret.Labels[constants.TeamLabel]).To(Equal(application.GetNamespace()))
		Expect(secret.GetNamespace()).To(Equal(application.GetNamespace()))
	})

	It("sets correlation id annotation and secret name", func() {
		application := aiven_nais_io_v1.NewAivenApplicationBuilder(applicationName, namespace).
			WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName}).
			WithAnnotation(nais_io_v1.DeploymentCorrelationIDAnnotation, correlationId).
			Build()
		secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace}}

		_, err := handler.ApplyIndividualSecret(ctx, &application, &secret, logger)
		Expect(err).To(Succeed())

		Expect(validation.ValidateAnnotations(secret.GetAnnotations(), field.NewPath("metadata.annotations"))).To(BeEmpty())
		Expect(secret.GetAnnotations()[nais_io_v1.DeploymentCorrelationIDAnnotation]).To(Equal(correlationId))
		Expect(secret.GetName()).To(Equal(application.Spec.SecretName))
	})

	It("merges labels/annotations with a pre-existing secret", func() {
		application := aiven_nais_io_v1.NewAivenApplicationBuilder(applicationName, namespace).
			WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName}).
			Build()
		secret := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:            secretName,
				Namespace:       namespace,
				Labels:          map[string]string{"pre-existing-label": "pre-existing-label"},
				Annotations:     map[string]string{"pre-existing-annotation": "pre-existing-annotation"},
				OwnerReferences: []metav1.OwnerReference{{Name: "pre-existing-owner-reference"}},
				Finalizers:      []string{"pre-existing-finalizer"},
			},
		}

		_, err := handler.ApplyIndividualSecret(ctx, &application, &secret, logger)
		Expect(err).To(Succeed())

		Expect(secret.Labels).Should(HaveKey("pre-existing-label"), "existing label missing")
		Expect(secret.Labels).Should(HaveKey(constants.AppLabel), "new label missing")
		Expect(secret.Annotations).Should(HaveKey("pre-existing-annotation"), "existing annotation missing")
		Expect(secret.Annotations).Should(HaveKey(nais_io_v1.DeploymentCorrelationIDAnnotation), "new annotation missing")
		Expect(secret.Finalizers).Should(ContainElement("pre-existing-finalizer"), "existing finalizer missing")
		Expect(secret.OwnerReferences).Should(ContainElement(metav1.OwnerReference{Name: "pre-existing-owner-reference"}), "pre-existing ownerReference missing")
		Expect(secret.OwnerReferences).Should(HaveLen(1), "additional ownerReferences set")
	})

	It("propagates aiven-generation label from application", func() {
		application := aiven_nais_io_v1.NewAivenApplicationBuilder(applicationName, namespace).
			WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName}).
			Build()
		application.Labels = map[string]string{constants.GenerationLabel: secretGeneration}
		secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace}}

		_, err := handler.ApplyIndividualSecret(ctx, &application, &secret, logger)
		Expect(err).To(Succeed())

		Expect(secret.Labels).Should(HaveKey(constants.GenerationLabel), "generation label missing")
		Expect(secret.Labels[constants.GenerationLabel]).To(Equal(secretGeneration))
	})

	It("marks protected secret in annotations and labels", func() {
		application := aiven_nais_io_v1.NewAivenApplicationBuilder(applicationName, namespace).
			WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName, Protected: true}).
			Build()
		secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace}}

		_, err := handler.ApplyIndividualSecret(ctx, &application, &secret, logger)
		Expect(err).To(Succeed())

		Expect(validation.ValidateAnnotations(secret.GetAnnotations(), field.NewPath("metadata.annotations"))).To(BeEmpty())
		Expect(secret.GetAnnotations()[constants.AivenatorProtectedKey]).To(Equal("true"))
		Expect(secret.GetLabels()[constants.AivenatorProtectedKey]).To(Equal("true"))
	})

	It("adds correct timestamp to secret data", func() {
		application := aiven_nais_io_v1.NewAivenApplicationBuilder(applicationName, namespace).
			WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName}).
			Build()
		secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace}}

		_, err := handler.ApplyIndividualSecret(ctx, &application, &secret, logger)
		Expect(err).To(Succeed())

		value := secret.StringData[AivenSecretUpdatedKey]
		timestamp, err := time.Parse(time.RFC3339, value)
		Expect(err).To(Succeed())
		Expect(timestamp).To(BeTemporally("~", time.Now(), 10*time.Second))
	})

	It("adds project CA to secret data", func() {
		application := aiven_nais_io_v1.NewAivenApplicationBuilder(applicationName, namespace).
			WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName}).
			Build()
		secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace}}

		_, err := handler.ApplyIndividualSecret(ctx, &application, &secret, logger)
		Expect(err).To(Succeed())

		value := secret.StringData[AivenCAKey]
		Expect(value).To(Equal(projectCA))
	})

	Describe("invalid secret name is unrecoverable", func() {
		It("rejects empty name", func() {
			application := aiven_nais_io_v1.NewAivenApplicationBuilder(applicationName, namespace).
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: ""}).
				Build()
			secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "", Namespace: namespace}}

			_, err := handler.ApplyIndividualSecret(ctx, &application, &secret, logger)
			Expect(err).ToNot(Succeed())
			Expect(errors.Is(err, utils.ErrUnrecoverable)).To(BeTrue())
		})

		It("rejects name with illegal characters", func() {
			application := aiven_nais_io_v1.NewAivenApplicationBuilder(applicationName, namespace).
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: "my_super_(c@@LS_ecE43109*23"}).
				Build()
			secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "my_super_(c@@LS_ecE43109*23", Namespace: namespace}}

			_, err := handler.ApplyIndividualSecret(ctx, &application, &secret, logger)
			Expect(err).ToNot(Succeed())
			Expect(errors.Is(err, utils.ErrUnrecoverable)).To(BeTrue())
		})

		It("rejects name with unicode digits", func() {
			application := aiven_nais_io_v1.NewAivenApplicationBuilder(applicationName, namespace).
				WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: "my_super_❶➁➌"}).
				Build()
			secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "my_super_❶➁➌", Namespace: namespace}}

			_, err := handler.ApplyIndividualSecret(ctx, &application, &secret, logger)
			Expect(err).ToNot(Succeed())
			Expect(errors.Is(err, utils.ErrUnrecoverable)).To(BeTrue())
		})
	})
})
