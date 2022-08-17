package metrics

import (
	"strconv"
	"time"

	"github.com/aiven/aiven-go-client"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	Namespace = "aivenator"

	LabelAivenOperation     = "operation"
	LabelNamespace          = "namespace"
	LabelPool               = "pool"
	LabelResourceType       = "resource_type"
	LabelStatus             = "status"
	LabelSyncState          = "synchronization_state"
	LabelProcessingReason   = "processing_reason"
	LabelSecretState        = "state"
	LabelUserNameConvention = "username_convention"
	LabelHandler            = "handler"
)

type Reason string

const (
	HashChanged           Reason = "HashChanged"
	MissingSecret         Reason = "MissingSecret"
	MissingOwnerReference Reason = "MissingOwnerReference"
)

func (r Reason) String() string {
	return string(r)
}

var (
	ApplicationsProcessed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name:      "aiven_applications_processed",
		Namespace: Namespace,
		Help:      "number of applications synchronized with aiven",
	}, []string{LabelSyncState})

	ApplicationsRequeued = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name:      "aiven_applications_requeued",
		Namespace: Namespace,
		Help:      "number of applications requeued for synchronization",
	}, []string{LabelSyncState})

	ApplicationProcessingTime = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:      "aiven_application_processing_time_seconds",
		Namespace: Namespace,
		Help:      "seconds from observed to synchronised successfully",
		Buckets:   prometheus.LinearBuckets(1.0, 2.0, 20),
	}, []string{LabelSyncState})

	HandlerProcessingTime = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:      "aiven_handler_processing_time_seconds",
		Namespace: Namespace,
		Help:      "seconds spent processing by handler",
		Buckets:   prometheus.LinearBuckets(1.0, 2.0, 20),
	}, []string{LabelHandler})

	ProcessingReason = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name:      "processing_reason",
		Namespace: Namespace,
		Help:      "reason for why this processing needs to happen",
	}, []string{LabelProcessingReason})

	ServiceUsersCreated = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name:      "service_users_created",
		Namespace: Namespace,
		Help:      "number of service users created",
	}, []string{LabelPool})

	ServiceUsersDeleted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name:      "service_users_deleted",
		Namespace: Namespace,
		Help:      "number of service users deleted",
	}, []string{LabelPool})

	ServiceUsersCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "service_users_count",
		Namespace: Namespace,
		Help:      "total count of service users",
	}, []string{LabelPool, LabelUserNameConvention})

	AivenLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:      "aiven_latency",
		Namespace: Namespace,
		Help:      "latency in aiven api operations",
		Buckets:   prometheus.ExponentialBuckets(0.02, 2, 14),
	}, []string{LabelAivenOperation, LabelStatus, LabelPool})

	KubernetesResourcesWritten = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name:      "kubernetes_resources_written",
		Namespace: Namespace,
		Help:      "number of kubernetes resources written to the cluster",
	}, []string{LabelNamespace, LabelResourceType})

	KubernetesResourcesDeleted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name:      "kubernetes_resources_deleted",
		Namespace: Namespace,
		Help:      "number of kubernetes resources deleted from the cluster",
	}, []string{LabelNamespace, LabelResourceType})

	SecretsManaged = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "secrets_managed",
		Namespace: Namespace,
		Help:      "number of secrets managed",
	}, []string{LabelNamespace, LabelSecretState})
)

func ObserveAivenLatency(operation, pool string, fun func() error) error {
	timer := time.Now()
	err := fun()
	used := time.Now().Sub(timer)
	status := 200
	if err != nil {
		aivenErr, ok := err.(aiven.Error)
		if ok {
			status = aivenErr.Status
		} else {
			status = 0
		}
	}
	AivenLatency.With(prometheus.Labels{
		LabelAivenOperation: operation,
		LabelPool:           pool,
		LabelStatus:         strconv.Itoa(status),
	}).Observe(used.Seconds())
	return err
}

func Register(registry prometheus.Registerer) {
	registry.MustRegister(
		AivenLatency,
		KubernetesResourcesWritten,
		KubernetesResourcesDeleted,
		ServiceUsersCreated,
		ServiceUsersDeleted,
		ApplicationsProcessed,
		ApplicationsRequeued,
		ApplicationProcessingTime,
		HandlerProcessingTime,
		SecretsManaged,
		ServiceUsersCount,
		ProcessingReason,
	)
}
