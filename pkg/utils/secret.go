package utils

import (
	"github.com/nais/aivenator/constants"
	"k8s.io/api/core/v1"
	"math"
	"strconv"
	"time"
)

func NextRequeueInterval(secret *v1.Secret, requeueInterval time.Duration) time.Duration {
	retries := GetSecretRetries(secret)
	factor := math.Pow(2, float64(retries))
	return time.Duration(factor) * requeueInterval
}

func GetSecretRetries(secret *v1.Secret) int64 {
	if value, ok := secret.GetAnnotations()[constants.AivenatorRetryCounterAnnotation]; ok {
		count, err := strconv.ParseInt(value, 0, 64)
		if err == nil {
			return count
		}
	}
	return 0
}

func SetSecretRetries(secret *v1.Secret, retries int64) {
	secret.GetAnnotations()[constants.AivenatorRetryCounterAnnotation] = strconv.FormatInt(retries, 10)
}
