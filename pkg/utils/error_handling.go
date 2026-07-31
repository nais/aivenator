package utils

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/aiven/aiven-go-client/v2"
	"github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	"github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/json"
)

var ErrUnrecoverable = errors.New("ErrUnrecoverable")
var ErrNotFound = errors.New("ErrNotFound")
var ErrNotReady = errors.New("ErrNotReady")

// Prefix for the per-secret pending-miss counters; a condition (not annotation) so writes
// can't trip AnnotationChangedPredicate.
const ConditionServiceUserSecretPending = aiven_nais_io_v1.AivenApplicationConditionType("ServiceUserSecretPending")

// ~20 min at the 100s ErrNotFound requeue cadence, past the longest legit cross-operator wait.
const SecretMissEscalateThreshold = 12

// FieldInvariant labels a log line with its instance-free cause, so tooling can group by it.
const FieldInvariant = "invariant"

func PendingSecretConditionType(secret string) aiven_nais_io_v1.AivenApplicationConditionType {
	return ConditionServiceUserSecretPending + aiven_nais_io_v1.AivenApplicationConditionType("/"+secret)
}

// All pending-counter conditions; dropTypes is exact-match, so success() needs the full list.
func PendingSecretConditionTypes(status *aiven_nais_io_v1.AivenApplicationStatus) []aiven_nais_io_v1.AivenApplicationConditionType {
	var types []aiven_nais_io_v1.AivenApplicationConditionType
	for _, c := range status.Conditions {
		if strings.HasPrefix(string(c.Type), string(ConditionServiceUserSecretPending)) {
			types = append(types, c.Type)
		}
	}
	return types
}

// ClearSecretPending drops secret's miss counter — call it when the secret appears.
func ClearSecretPending(application *aiven_nais_io_v1.AivenApplication, secret string) {
	condType := PendingSecretConditionType(secret)
	kept := make([]aiven_nais_io_v1.AivenApplicationCondition, 0, len(application.Status.Conditions))
	for _, c := range application.Status.Conditions {
		if c.Type != condType {
			kept = append(kept, c)
		}
	}
	application.Status.Conditions = kept
}

// SecretNotReadyError: aiven-operator hasn't published the ServiceUser secret yet. Unwraps to
// ErrNotFound (still requeues); escalates Warn→Error once misses reach the threshold.
type SecretNotReadyError struct {
	Namespace string
	Secret    string
	Escalated bool
}

func (e *SecretNotReadyError) Error() string {
	return fmt.Sprintf("serviceuser secret %s/%s not found: %v", e.Namespace, e.Secret, ErrNotFound)
}

// Invariant is the name-free label shared by all pending-secret waits.
func (e *SecretNotReadyError) Invariant() string {
	return "serviceuser secret not found"
}

func (e *SecretNotReadyError) Unwrap() error {
	return ErrNotFound
}

// ReportFailure logs at Warn only when every leaf of err is a non-escalated pending-secret
// wait, else Error; tags FieldInvariant from err if it has one, else invariantDefault (empty omits).
func ReportFailure(logger logrus.FieldLogger, err error, invariantDefault string, message interface{}) {
	invariant := invariantDefault
	var named interface{ Invariant() string }
	if errors.As(err, &named) {
		invariant = named.Invariant()
	}
	entry := logger
	if invariant != "" {
		entry = logger.WithField(FieldInvariant, invariant)
	}

	if allPendingSecret(err) {
		entry.Warn(message)
		return
	}
	entry.Error(message)
}

// allPendingSecret: every leaf of err is a non-escalated SecretNotReadyError.
func allPendingSecret(err error) bool {
	if err == nil {
		return false
	}
	if nr, ok := err.(*SecretNotReadyError); ok {
		return !nr.Escalated
	}
	switch e := err.(type) {
	case interface{ Unwrap() []error }:
		subs := e.Unwrap()
		if len(subs) == 0 {
			return false
		}
		for _, s := range subs {
			if !allPendingSecret(s) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return allPendingSecret(e.Unwrap())
	default:
		return false
	}
}

func AivenFail(operation string, application *aiven_nais_io_v1.AivenApplication, err error, notFoundIsRecoverable bool, logger logrus.FieldLogger) error {
	errorMessage := UnwrapAivenError(err, logger, notFoundIsRecoverable)
	recordSecretPending(application, errorMessage) // sets Escalated before ReportFailure reads it
	message := fmt.Errorf("operation %s failed in Aiven: %w", operation, errorMessage)
	ReportFailure(logger, errorMessage, fmt.Sprintf("operation %s failed in Aiven", operation), message)
	application.Status.AddCondition(aiven_nais_io_v1.AivenApplicationCondition{
		Type:    aiven_nais_io_v1.AivenApplicationAivenFailure,
		Status:  v1.ConditionTrue,
		Reason:  operation,
		Message: message.Error(),
	}, aiven_nais_io_v1.AivenApplicationSucceeded)
	return message
}

// recordSecretPending bumps the per-secret miss count and escalates at the threshold.
func recordSecretPending(application *aiven_nais_io_v1.AivenApplication, err error) {
	var notReady *SecretNotReadyError
	if !errors.As(err, &notReady) {
		return
	}
	condType := PendingSecretConditionType(notReady.Secret)
	count := 1
	if cond := application.Status.GetConditionOfType(condType); cond != nil {
		if n, parseErr := strconv.Atoi(cond.Reason); parseErr == nil {
			count = n + 1
		}
	}
	notReady.Escalated = count >= SecretMissEscalateThreshold
	application.Status.AddCondition(aiven_nais_io_v1.AivenApplicationCondition{
		Type:    condType,
		Status:  v1.ConditionTrue,
		Reason:  strconv.Itoa(count),
		Message: notReady.Error(),
	})
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

// LocalFail records and logs operation's failure, labelled with its invariant.
func LocalFail(operation string, application *aiven_nais_io_v1.AivenApplication, err error, logger logrus.FieldLogger) {
	localFail(operation, application, err, logger, fmt.Sprintf("operation %s failed", operation))
}

// LocalFailBeforeApply is LocalFail without the label, for reconciler failures before any Apply().
func LocalFailBeforeApply(operation string, application *aiven_nais_io_v1.AivenApplication, err error, logger logrus.FieldLogger) {
	localFail(operation, application, err, logger, "")
}

func localFail(operation string, application *aiven_nais_io_v1.AivenApplication, err error, logger logrus.FieldLogger, invariant string) {
	message := fmt.Errorf("operation %s failed: %s", operation, err)
	ReportFailure(logger, err, invariant, message)
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
