package secret_test

import (
	"errors"
	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/handlers/secret"
	"github.com/nais/aivenator/pkg/utils"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
	"time"
)

const (
	namespace       = "ns"
	applicationName = "app"
	secretName      = "my-secret"
	correlationId   = "correlation-id"
)

func TestSecret(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Secret Suite")
}

var _ = Describe("secret.Handler", func() {
	exampleAivenApplication := aiven_nais_io_v1.NewAivenApplicationBuilder(applicationName, namespace).
		WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName}).
		Build()
	var handler secret.Handler

	type args struct {
		application aiven_nais_io_v1.AivenApplication
		secret      corev1.Secret
		assert      func(args)
	}

	BeforeEach(func() {
		handler = secret.Handler{}
	})

	DescribeTable("correctly handles", func(args args) {
		err := handler.Apply(&args.application, &args.secret, nil)
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
		Entry("a protected secret",
			args{
				application: aiven_nais_io_v1.NewAivenApplicationBuilder(applicationName, namespace).
					WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName, Protected: true}).
					Build(),
				secret: corev1.Secret{},
				assert: func(a args) {
					Expect(a.secret.GetAnnotations()[constants.AivenatorProtectedAnnotation]).To(Equal("true"))
				},
			}),
	)

	It("adds correct timestamp to secret data", func() {
		s := corev1.Secret{}
		err := handler.Apply(&exampleAivenApplication, &s, nil)
		Expect(err).To(Succeed())
		value := s.StringData[secret.AivenSecretUpdatedKey]
		timestamp, err := time.Parse(time.RFC3339, value)
		Expect(err).To(Succeed())

		Expect(timestamp).To(BeTemporally("~", time.Now(), 10*time.Second))
	})

	DescribeTable("returns unrecoverable errors for invalid secret name:", func(secretName string) {
		application := aiven_nais_io_v1.NewAivenApplicationBuilder(applicationName, namespace).
			WithSpec(aiven_nais_io_v1.AivenApplicationSpec{SecretName: secretName}).
			Build()
		err := handler.Apply(&application, &corev1.Secret{}, nil)
		Expect(err).ToNot(Succeed())
		Expect(errors.Is(err, utils.UnrecoverableError)).To(BeTrue())
	},
		EntryDescription("%v"),
		Entry("<empty>", ""),
		Entry(nil, "my_super_(c@@LS_ecE43109*23"),
		Entry(nil, "my_super_❶➁➌"),
	)
})
