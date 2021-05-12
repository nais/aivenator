package utils

import (
	"fmt"
	"github.com/aiven/aiven-go-client"
	"github.com/nais/liberator/pkg/apis/kafka.nais.io/v1"
	"github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/json"
)

func AivenFail(operation string, application *kafka_nais_io_v1.AivenApplication, err error, logger *logrus.Entry) error {
	errorMessage := err
	if aivenErr, ok := err.(aiven.Error); ok {
		apiMessage := struct {
			Message string `json:"message"`
		}{}
		err := json.Unmarshal([]byte(aivenErr.Message), &apiMessage)
		if err != nil {
			errorMessage = fmt.Errorf("got aiven error %s, but failed to decompose as JSON: %s", aivenErr, err)
		} else {
			errorMessage = fmt.Errorf("%s", apiMessage.Message)
		}
	}
	message := fmt.Errorf("operation %s failed in Aiven: %s", operation, errorMessage)
	logger.Error(message)
	application.Status.AddCondition(kafka_nais_io_v1.AivenApplicationCondition{
		Type:    kafka_nais_io_v1.AivenApplicationAivenFailure,
		Status:  v1.ConditionTrue,
		Reason:  operation,
		Message: message.Error(),
	}, kafka_nais_io_v1.AivenApplicationSucceeded)
	return message
}

func LocalFail(operation string, application *kafka_nais_io_v1.AivenApplication, err error, logger *logrus.Entry) {
	message := fmt.Errorf("operation %s failed: %s", operation, err)
	logger.Error(message)
	application.Status.AddCondition(kafka_nais_io_v1.AivenApplicationCondition{
		Type:    kafka_nais_io_v1.AivenApplicationLocalFailure,
		Status:  v1.ConditionTrue,
		Reason:  operation,
		Message: message.Error(),
	}, kafka_nais_io_v1.AivenApplicationSucceeded)
}
