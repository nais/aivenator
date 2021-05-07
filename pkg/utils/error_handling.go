package utils

import (
	"fmt"
	"github.com/nais/liberator/pkg/apis/kafka.nais.io/v1"
	"github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
)

func AivenFail(operation string, application *kafka_nais_io_v1.AivenApplication, err error, logger *logrus.Entry) {
	message := fmt.Errorf("operation %s failed in Aiven: %s", operation, err)
	logger.Error(message)
	application.Status.AddCondition(kafka_nais_io_v1.AivenApplicationCondition{
		Type:    kafka_nais_io_v1.AivenApplicationAivenFailure,
		Status:  v1.ConditionTrue,
		Reason:  operation,
		Message: message.Error(),
	})
}

func LocalFail(operation string, application *kafka_nais_io_v1.AivenApplication, err error, logger *logrus.Entry) {
	message := fmt.Errorf("operation %s failed: %s", operation, err)
	logger.Error(message)
	application.Status.AddCondition(kafka_nais_io_v1.AivenApplicationCondition{
		Type:    kafka_nais_io_v1.AivenApplicationLocalFailure,
		Status:  v1.ConditionTrue,
		Reason:  operation,
		Message: message.Error(),
	})
}
