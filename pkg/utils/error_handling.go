package utils

import (
	"errors"
	"fmt"

	"github.com/aiven/aiven-go-client"
	"github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	"github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/json"
)

var UnrecoverableError = errors.New("UnrecoverableError")

func AivenFail(operation string, application *aiven_nais_io_v1.AivenApplication, err error, logger logrus.FieldLogger) error {
	errorMessage := UnwrapAivenError(err, logger)
	message := fmt.Errorf("operation %s failed in Aiven: %w", operation, errorMessage)
	logger.Error(message)
	application.Status.AddCondition(aiven_nais_io_v1.AivenApplicationCondition{
		Type:    aiven_nais_io_v1.AivenApplicationAivenFailure,
		Status:  v1.ConditionTrue,
		Reason:  operation,
		Message: message.Error(),
	}, aiven_nais_io_v1.AivenApplicationSucceeded)
	return message
}

func UnwrapAivenError(errorMessage error, logger logrus.FieldLogger) error {
	if aivenErr, ok := errorMessage.(aiven.Error); ok {
		apiMessage := struct {
			Message string `json:"message"`
		}{}
		var message string
		err := json.Unmarshal([]byte(aivenErr.Message), &apiMessage)
		if err != nil {
			logger.Warnf("got aiven error %s, but failed to decompose '%s' as JSON: %s", aivenErr, aivenErr.Message, err)
			message = aivenErr.Error()
		} else {
			message = apiMessage.Message
		}
		if 400 <= aivenErr.Status && aivenErr.Status < 500 {
			return fmt.Errorf("%s: %w", message, UnrecoverableError)
		} else {
			return fmt.Errorf("%s", message)
		}
	}
	return errorMessage
}

func LocalFail(operation string, application *aiven_nais_io_v1.AivenApplication, err error, logger logrus.FieldLogger) {
	message := fmt.Errorf("operation %s failed: %s", operation, err)
	logger.Error(message)
	application.Status.AddCondition(aiven_nais_io_v1.AivenApplicationCondition{
		Type:    aiven_nais_io_v1.AivenApplicationLocalFailure,
		Status:  v1.ConditionTrue,
		Reason:  operation,
		Message: message.Error(),
	}, aiven_nais_io_v1.AivenApplicationSucceeded)
}
