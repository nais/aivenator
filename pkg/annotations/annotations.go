package annotations

import (
	"github.com/nais/aivenator/constants"
	"github.com/nais/aivenator/pkg/handlers/kafka"
	"github.com/nais/aivenator/pkg/utils"
	aiven_nais_io_v1 "github.com/nais/liberator/pkg/apis/aiven.nais.io/v1"
	"strconv"
)

func hasAnnotation(annotations map[string]string, key string) (string, bool) {
	value, found := annotations[key]
	return value, found
}

func HasProtected(annotations map[string]string) bool {
	value, found := hasAnnotation(annotations, constants.AivenatorProtectedAnnotation)
	return found && value == "true"
}

func HasTimeLimited(annotations map[string]string) bool {
	value, found := hasAnnotation(annotations, constants.AivenatorProtectedExpireAtAnnotation)
	return found && value == "true"
}

func HasDelete(annotations map[string]string) bool {
	value, found := hasAnnotation(annotations, kafka.DeleteExpiredAnnotation)
	return found && value == "true"
}

func SetDelete(application aiven_nais_io_v1.AivenApplication) {
	application.SetAnnotations(utils.MergeStringMap(application.GetAnnotations(), map[string]string{
		kafka.DeleteExpiredAnnotation: strconv.FormatBool(true),
	}))
}
