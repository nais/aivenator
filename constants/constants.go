package constants

const (
	AivenatorFinalizer = "aivenator.aiven.nais.io/finalizer"

	AppLabel        = "app"
	TeamLabel       = "team"
	SecretTypeLabel = "type"
	GenerationLabel = "aiven.nais.io/secret-generation"

	AivenatorProtectedKey                     = "aivenator.aiven.nais.io/protected"
	AivenatorProtectedWithTimeLimitAnnotation = "aivenator.aiven.nais.io/with-time-limit"
	AivenatorProtectedExpiresAtAnnotation     = "aivenator.aiven.nais.io/expires-at"
	AivenatorRetryCounterAnnotation           = "aivenator.aiven.nais.io/retries"

	AivenatorSecretType = "aivenator.aiven.nais.io"
)
