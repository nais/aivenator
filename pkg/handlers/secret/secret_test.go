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
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

var _ = Describe("secret.Handler", func() {
	exampleAivenApplication := aiven_nais_io_v1.NewAivenApplicationBuilder(applicationName, namespace).
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName}).
		Build()
	applicationWithGeneration := aiven_nais_io_v1.NewAivenApplicationBuilder(applicationName, namespace).
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName}).
		Build()
	applicationWithGeneration.Labels = map[string]string{constants.GenerationLabel: secretGeneration}
	var handler Handler
	var mockProjects *project.MockProjectManager
	var mockK8s client.Client
	var ctx context.Context
	var cancel context.CancelFunc

	type args struct {
		application aiven_nais_io_v1.AivenApplication
		secret      corev1.Secret
		assert      func(args)
	}

	BeforeEach(func() {
		mockProjects = project.NewMockProjectManager(GinkgoT())
		mockProjects.On("GetCA", mock.Anything, projectName).Return(projectCA, nil).Maybe()
		handler = Handler{
			mockProjects,
			projectName,
			mockK8s,
		}
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	})

	AfterEach(func() {
		cancel()
	})

	DescribeTable("correctly handles", func(args args) {
		err := handler.NormalizeSecret(ctx, &args.application, &args.secret, args.secret.Name, nil)
		Expect(err).To(Succeed())

		args.assert(args)
	},
		Entry("a basic AivenApplication",

			args{
				application: exampleAivenApplication,
				secret:      corev1.Secret{},

				assert: func(a args) {
					Expect(a.secret.Labels[constants.SecretTypeLabel]).To(Equal(constants.AivenatorSecretType))
					Expect(a.secret.Labels[constants.AppLabel]).To(Equal(a.application.GetName()))
					Expect(a.secret.Labels[constants.TeamLabel]).To(Equal(a.application.GetNamespace()))
					Expect(a.secret.GetNamespace()).To(Equal(a.application.GetNamespace()))
				},
			}),
		Entry("an AivenApplication with secret and correlationId",
			args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder(applicationName, namespace).
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName}).
					WithAnnotation(nais_io_v1.DeploymentCorrelationIDAnnotation, correlationId).
					Build(),
				secret: corev1.Secret{},
				assert: func(a args) {
					Expect(validation.ValidateAnnotations(a.secret.GetAnnotations(), field.NewPath("metadata.annotations"))).To(BeEmpty())
					Expect(a.secret.GetAnnotations()[nais_io_v1.DeploymentCorrelationIDAnnotation]).To(Equal(correlationId))
					Expect(a.secret.GetName()).To(Equal(a.application.Spec.SecretName))
				},
			}),
		Entry("a pre-existing secret",
			args{
				application: exampleAivenApplication,
				secret: corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:            secretName,
						Namespace:       namespace,
						Labels:          map[string]string{"pre-existing-label": "pre-existing-label"},
						Annotations:     map[string]string{"pre-existing-annotation": "pre-existing-annotation"},
						OwnerReferences: []metav1.OwnerReference{{Name: "pre-existing-owner-reference"}},
						Finalizers:      []string{"pre-existing-finalizer"},
					},
				},
				assert: func(a args) {
					Expect(a.secret.Labels).Should(HaveKey("pre-existing-label"), "existing label missing")
					Expect(a.secret.Labels).Should(HaveKey(constants.AppLabel), "new label missing")
					Expect(a.secret.Annotations).Should(HaveKey("pre-existing-annotation"), "existing annotation missing")
					Expect(a.secret.Annotations).Should(HaveKey(nais_io_v1.DeploymentCorrelationIDAnnotation), "new annotation missing")

					Expect(a.secret.Finalizers).Should(ContainElement("pre-existing-finalizer"), "existing finalizer missing")
					Expect(a.secret.OwnerReferences).Should(ContainElement(metav1.OwnerReference{Name: "pre-existing-owner-reference"}), "pre-existing ownerReference missing")

					Expect(a.secret.OwnerReferences).Should(HaveLen(1), "additional ownerReferences set")
				},
			}),
		Entry("a aiven-generation label is set on application",
			args{
				application: applicationWithGeneration,
				secret:      corev1.Secret{},
				assert: func(a args) {
					Expect(a.secret.Labels).Should(HaveKey(constants.GenerationLabel), "generation label missing")
					Expect(a.secret.Labels[constants.GenerationLabel]).To(Equal(secretGeneration))
				},
			}),
		Entry("a protected secret",
			args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder(applicationName, namespace).
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName, Protected: true}).
					Build(),
				secret: corev1.Secret{},
				assert: func(a args) {
					Expect(validation.ValidateAnnotations(a.secret.GetAnnotations(), field.NewPath("metadata.annotations"))).To(BeEmpty())
					Expect(a.secret.GetAnnotations()[constants.AivenatorProtectedKey]).To(Equal("true"))
					Expect(a.secret.GetLabels()[constants.AivenatorProtectedKey]).To(Equal("true"))
				},
			}),
	)

	It("adds correct timestamp to secret data", func() {
		s := corev1.Secret{}
		err := handler.NormalizeSecret(ctx, &exampleAivenApplication, &s, secretName, nil)

		Expect(err).To(Succeed())
		value := s.StringData[AivenSecretUpdatedKey]
		timestamp, err := time.Parse(time.RFC3339, value)
		Expect(err).To(Succeed())

		Expect(timestamp).To(BeTemporally("~", time.Now(), 10*time.Second))
	})

	It("adds project CA to secret data", func() {
		s := corev1.Secret{}
		err := handler.NormalizeSecret(ctx, &exampleAivenApplication, &s, secretName, nil)

		Expect(err).To(Succeed())
		value := s.StringData[AivenCAKey]

		Expect(value).To(Equal(projectCA))
	})

	DescribeTable("returns unrecoverable errors for invalid secret name:", func(secretName string) {
		s := corev1.Secret{}

		application := aiven_nais_io_v1.NewAivenApplicationBuilder(applicationName, namespace).
			WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName}).
			Build()

		err := handler.NormalizeSecret(ctx, &application, &s, secretName, nil)

		Expect(err).ToNot(Succeed())
		Expect(errors.Is(err, utils.ErrUnrecoverable)).To(BeTrue())
	},
		EntryDescription("%v"),
		Entry("<empty>", ""),
		Entry(nil, "my_super_(c@@LS_ecE43109*23"),
		Entry(nil, "my_super_❶➁➌"),
	)
})
