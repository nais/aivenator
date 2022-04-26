package utils

import (
	"github.com/nais/aivenator/constants"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
	"time"
)

const requeueInterval = 10 * time.Second

func TestGetSecretRetries(t *testing.T) {
	tests := []struct {
		name   string
		secret *corev1.Secret
		want   int64
	}{
		{
			"No retries",
			&corev1.Secret{},
			0,
		},
		{
			"Many retries",
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						constants.AivenatorRetryCounterAnnotation: "15",
					},
				},
			},
			15,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetSecretRetries(tt.secret); got != tt.want {
				t.Errorf("GetSecretRetries() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNextRequeueInterval(t *testing.T) {
	tests := []struct {
		name   string
		secret *corev1.Secret
		want   time.Duration
	}{
		{
			"No retries",
			&corev1.Secret{},
			10 * time.Second,
		},
		{
			"2 retries",
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{constants.AivenatorRetryCounterAnnotation: "2"},
				},
			},
			40 * time.Second,
		},
		{
			"5 retries",
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{constants.AivenatorRetryCounterAnnotation: "5"},
				},
			},
			320 * time.Second,
		},
		{
			"9 retries",
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{constants.AivenatorRetryCounterAnnotation: "9"},
				},
			},
			5120 * time.Second,
		},
		{
			"10 retries, give up",
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{constants.AivenatorRetryCounterAnnotation: "10"},
				},
			},
			0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := NextRequeueInterval(tt.secret, requeueInterval)
			assert.InDelta(t, tt.want, actual, float64(tt.want)*0.1, "NextRequeueInterval() = %v, want %v", actual, tt.want)
		})
	}
}
