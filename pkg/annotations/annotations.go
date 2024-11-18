package annotations

import (
	"github.com/nais/aivenator/constants"
)

func hasAnnotation(annotations map[string]string, key string) (string, bool) {
	value, found := annotations[key]
	return value, found
}

func HasProtected(annotations map[string]string) bool {
	value, found := hasAnnotation(annotations, constants.AivenatorProtectedKey)
	return found && value == "true"
}

func HasTimeLimited(annotations map[string]string) bool {
	value, found := hasAnnotation(annotations, constants.AivenatorProtectedWithTimeLimitAnnotation)
	return found && value == "true"
}
