package credentials

import (
	"context"
	"fmt"
	"maps"

	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("Manager", func() {
	var application aiven_nais_io_v1.AivenApplication
	var logger log.FieldLogger

	BeforeEach(func() {
		application = aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").Build()
		root := log.New()
		root.Out = GinkgoWriter
		logger = log.NewEntry(root)
	})

	It("Apply propagates annotations to shared and individual secrets", func() {
		mockHandler := MockHandler{}
		expectedAnnotations := map[string]string{"one": "1"}

		mockHandler.
			On("Apply",
				mock.Anything,
				mock.AnythingOfType("*aiven_nais_io_v1.AivenApplication"),
				mock.AnythingOfType("*v1.Secret"),
				mock.Anything).
			Return(nil, nil).
			Run(func(args mock.Arguments) {
				secret := args.Get(2).(*corev1.Secret)
				secret.ObjectMeta.Annotations = make(map[string]string, len(expectedAnnotations))
				maps.Copy(secret.ObjectMeta.Annotations, expectedAnnotations)
			})

		manager := Manager{handlers: []Handler{&mockHandler}}

		sharedSecret := &corev1.Secret{}
		individualSecrets, err := manager.CreateSecret(context.Background(), &application, sharedSecret, logger)

		Expect(err).NotTo(HaveOccurred())
		Expect(individualSecrets).NotTo(BeEmpty())
		Expect(sharedSecret.ObjectMeta.Annotations).To(Equal(expectedAnnotations))
		Expect(individualSecrets[0].ObjectMeta.Annotations).To(Equal(expectedAnnotations))
	})

	It("ApplyFailed cleans up on error", func() {
		mockHandler := MockHandler{}
		failingHandler := MockHandler{}
		expectedAnnotations := map[string]string{"one": "1"}

		mockHandler.
			On("Apply",
				mock.Anything,
				mock.AnythingOfType("*aiven_nais_io_v1.AivenApplication"),
				mock.AnythingOfType("*v1.Secret"),
				mock.Anything).
			Return(nil, nil).
			Run(func(args mock.Arguments) {
				secret := args.Get(2).(*corev1.Secret)
				secret.ObjectMeta.Annotations = make(map[string]string, len(expectedAnnotations))
				maps.Copy(secret.ObjectMeta.Annotations, expectedAnnotations)
			})

		mockHandler.
			On("Cleanup",
				mock.Anything,
				mock.AnythingOfType("*v1.Secret"),
				mock.Anything).
			Return(nil)

		handlerError := fmt.Errorf("failing handler")

		failingHandler.
			On("Apply",
				mock.Anything,
				mock.AnythingOfType("*aiven_nais_io_v1.AivenApplication"),
				mock.AnythingOfType("*v1.Secret"),
				mock.Anything).
			Return(nil, handlerError)

		failingHandler.
			On("Cleanup",
				mock.Anything,
				mock.AnythingOfType("*v1.Secret"),
				mock.Anything).
			Return(nil)

		manager := Manager{handlers: []Handler{&mockHandler, &failingHandler}}

		secret := &corev1.Secret{}
		_, err := manager.CreateSecret(context.Background(), &application, secret, logger)

		Expect(err).To(MatchError(handlerError))

		failingHandler.AssertCalled(GinkgoT(), "Cleanup",
			mock.Anything,
			mock.AnythingOfType("*v1.Secret"),
			mock.Anything)
	})

	It("Cleanup delegates to handlers", func() {
		mockHandler := MockHandler{}
		mockHandler.
			On("Cleanup",
				mock.Anything,
				mock.AnythingOfType("*v1.Secret"),
				mock.Anything).
			Return(nil)

		manager := Manager{handlers: []Handler{&mockHandler}}
		secret := corev1.Secret{}

		err := manager.Cleanup(context.Background(), &secret, logger)
		Expect(err).NotTo(HaveOccurred())

		mockHandler.AssertCalled(GinkgoT(), "Cleanup",
			mock.Anything,
			mock.AnythingOfType("*v1.Secret"),
			mock.Anything)
	})
})
