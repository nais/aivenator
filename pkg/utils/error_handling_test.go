package utils

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/aiven/aiven-go-client/v2"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("containsPossibleCredentials", func() {
	DescribeTable("returns true for trigger words",
		func(word string) {
			err := fmt.Errorf("some error containing %s here", word)
			Expect(containsPossibleCredentials(err)).To(BeTrue())
		},
		Entry("password", "password"),
		Entry("token", "token"),
		Entry("secret", "secret"),
		Entry("private key", "private key"),
		Entry("certificate", "certificate"),
		Entry("avns_", "avns_"),
	)

	DescribeTable("case insensitive",
		func(word string) {
			err := fmt.Errorf("some error containing %s here", word)
			Expect(containsPossibleCredentials(err)).To(BeTrue())
		},
		Entry("PASSWORD", "PASSWORD"),
		Entry("Token", "Token"),
		Entry("SECRET", "SECRET"),
		Entry("Private Key", "Private Key"),
		Entry("Certificate", "Certificate"),
		Entry("AVNS_", "AVNS_"),
	)

	It("returns false for clean error messages", func() {
		err := fmt.Errorf("service not found in project")
		Expect(containsPossibleCredentials(err)).To(BeFalse())
	})
})

var _ = Describe("UnwrapAivenError", func() {
	var logger *logrus.Logger

	BeforeEach(func() {
		logger = logrus.New()
		logger.Out = GinkgoWriter
	})

	It("passes through non-Aiven errors unchanged", func() {
		original := fmt.Errorf("some local error")
		result := UnwrapAivenError(original, logger, false)
		Expect(result).To(Equal(original))
	})

	It("returns generic message for StatusOK Aiven error", func() {
		err := aiven.Error{Status: http.StatusOK, Message: "some body content"}
		result := UnwrapAivenError(err, logger, false)
		Expect(result.Error()).To(ContainSubstring("unknown error"))
		Expect(errors.Is(result, ErrUnrecoverable)).To(BeFalse())
		Expect(errors.Is(result, ErrNotFound)).To(BeFalse())
	})

	It("scrubs message containing credentials", func() {
		err := aiven.Error{Status: 500, Message: `{"message": "password=supersecret"}`, MoreInfo: "password=supersecret"}
		result := UnwrapAivenError(err, logger, false)
		Expect(result.Error()).ToNot(ContainSubstring("supersecret"))
	})

	It("extracts message field from valid JSON", func() {
		err := aiven.Error{Status: 400, Message: `{"message": "user not found"}`, MoreInfo: ""}
		result := UnwrapAivenError(err, logger, false)
		Expect(result.Error()).To(ContainSubstring("user not found"))
	})

	It("falls back to Error() for invalid JSON message", func() {
		err := aiven.Error{Status: 400, Message: "not json at all", MoreInfo: ""}
		result := UnwrapAivenError(err, logger, false)
		Expect(result.Error()).To(ContainSubstring("not json at all"))
	})

	It("wraps ErrNotFound for 404 when notFoundIsRecoverable=true", func() {
		err := aiven.Error{Status: 404, Message: `{"message": "not found"}`, MoreInfo: ""}
		result := UnwrapAivenError(err, logger, true)
		Expect(errors.Is(result, ErrNotFound)).To(BeTrue())
	})

	It("wraps ErrUnrecoverable for 404 when notFoundIsRecoverable=false", func() {
		err := aiven.Error{Status: 404, Message: `{"message": "not found"}`, MoreInfo: ""}
		result := UnwrapAivenError(err, logger, false)
		Expect(errors.Is(result, ErrUnrecoverable)).To(BeTrue())
	})

	It("wraps ErrUnrecoverable for 4xx non-404", func() {
		err := aiven.Error{Status: 403, Message: `{"message": "forbidden"}`, MoreInfo: ""}
		result := UnwrapAivenError(err, logger, false)
		Expect(errors.Is(result, ErrUnrecoverable)).To(BeTrue())
	})

	It("does not wrap sentinel for 5xx errors", func() {
		err := aiven.Error{Status: 503, Message: `{"message": "service unavailable"}`, MoreInfo: ""}
		result := UnwrapAivenError(err, logger, false)
		Expect(errors.Is(result, ErrUnrecoverable)).To(BeFalse())
		Expect(errors.Is(result, ErrNotFound)).To(BeFalse())
	})
})

var _ = Describe("AivenFail", func() {
	var logger *logrus.Logger
	var application *aiven_nais_io_v1.AivenApplication

	BeforeEach(func() {
		logger = logrus.New()
		logger.Out = GinkgoWriter
		app := aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").Build()
		application = &app
	})

	It("returns an error", func() {
		result := AivenFail("create", application, fmt.Errorf("boom"), false, logger)
		Expect(result).To(HaveOccurred())
	})

	It("adds AivenFailure condition to application status", func() {
		AivenFail("create", application, fmt.Errorf("boom"), false, logger)
		conditions := application.Status.Conditions
		found := false
		for _, c := range conditions {
			if c.Type == aiven_nais_io_v1.AivenApplicationAivenFailure {
				found = true
			}
		}
		Expect(found).To(BeTrue())
	})

	It("clears Succeeded condition", func() {
		application.Status.AddCondition(aiven_nais_io_v1.AivenApplicationCondition{
			Type:   aiven_nais_io_v1.AivenApplicationSucceeded,
			Status: corev1.ConditionTrue,
		})
		AivenFail("create", application, fmt.Errorf("boom"), false, logger)
		for _, c := range application.Status.Conditions {
			Expect(c.Type).ToNot(Equal(aiven_nais_io_v1.AivenApplicationSucceeded))
		}
	})
})

var _ = Describe("LocalFail", func() {
	var logger *logrus.Logger
	var application *aiven_nais_io_v1.AivenApplication

	BeforeEach(func() {
		logger = logrus.New()
		logger.Out = GinkgoWriter
		app := aiven_nais_io_v1.NewAivenApplicationBuilder("app", "ns").Build()
		application = &app
	})

	It("adds LocalFailure condition to application status", func() {
		LocalFail("sync", application, fmt.Errorf("oops"), logger)
		found := false
		for _, c := range application.Status.Conditions {
			if c.Type == aiven_nais_io_v1.AivenApplicationLocalFailure {
				found = true
			}
		}
		Expect(found).To(BeTrue())
	})

	It("clears Succeeded condition", func() {
		application.Status.AddCondition(aiven_nais_io_v1.AivenApplicationCondition{
			Type:   aiven_nais_io_v1.AivenApplicationSucceeded,
			Status: corev1.ConditionTrue,
		})
		LocalFail("sync", application, fmt.Errorf("oops"), logger)
		for _, c := range application.Status.Conditions {
			Expect(c.Type).ToNot(Equal(aiven_nais_io_v1.AivenApplicationSucceeded))
		}
	})
})
