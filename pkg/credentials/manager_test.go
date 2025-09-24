package credentials

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("Manager", func() {
	var logger log.FieldLogger

	BeforeEach(func() {
		root := log.New()
		root.Out = GinkgoWriter
		logger = log.NewEntry(root)
	})

	It("Cleanup delegates to handlers", func() {
		mockHandler := MockHandler{}
		mockHandler.
			On("Cleanup",
				mock.Anything,
				mock.AnythingOfType("*v1.Secret"),
				mock.Anything).
			Return(nil)

		manager := Manager{handlers: []ServiceHandler{&mockHandler}}
		secret := corev1.Secret{}

		err := manager.Cleanup(context.Background(), &secret, logger)
		Expect(err).NotTo(HaveOccurred())

		mockHandler.AssertCalled(GinkgoT(), "Cleanup",
			mock.Anything,
			mock.AnythingOfType("*v1.Secret"),
			mock.Anything)
	})
})
