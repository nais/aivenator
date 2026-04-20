package utils

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	"github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/json"
)

var ErrUnrecoverable = errors.New("ErrUnrecoverable")
var ErrNotFound = errors.New("ErrNotFound")

func AivenFail(operation string, application *aiven_nais_io_v1.AivenApplication, err error, notFoundIsRecoverable bool, logger logrus.FieldLogger) error {
	errorMessage := UnwrapAivenError(err, logger, notFoundIsRecoverable)
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

func UnwrapAivenError(errorMessage error, logger logrus.FieldLogger, notFoundIsRecoverable bool) error {
	aivenErr := &aiven.Error{}
	if ok := errors.As(errorMessage, aivenErr); ok {
		// In rare cases, the Aiven client can return an error with StatusOK.
		// In these cases, the actual content of the error is not really relevant, because it is simply the response body
		// while the error was something related to I/O.
		// Since the response body may contain sensitive information, we do not want to log the message in this situation.
		if aivenErr.Status == http.StatusOK {
			return fmt.Errorf("unknown error while calling Aiven API")
		}

		if containsPossibleCredentials(aivenErr) {
			logger.Warnf("Encountered an error that could contain credentials. The body of the error has been discarded.")
			aivenErr.Message = "{\"msg\": \"<this message contained credentials and was discarded for safety>\"}"
			aivenErr.MoreInfo = "<this message contained credentials and was discarded for safety>"
		}

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
		if aivenErr.Status == 404 && notFoundIsRecoverable {
			return fmt.Errorf("%s: %w", message, ErrNotFound)
		}
		if 400 <= aivenErr.Status && aivenErr.Status < 500 {
			return fmt.Errorf("%s: %w", message, ErrUnrecoverable)
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

var triggerWords = []string{
	"password",
	"token",
	"secret",
	"private key",
	"certificate",
	"avns_",
}

// containsPossibleCredentials checks if an error message contains things that looks like credentials
func containsPossibleCredentials(err error) bool {
	lowerErr := strings.ToLower(err.Error())
	for _, trigger := range triggerWords {
		if strings.Contains(lowerErr, trigger) {
			return true
		}
	}
	return false
}
