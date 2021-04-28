package metrics

import (
	"strconv"
	"time"

	"github.com/aiven/aiven-go-client"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	Namespace = "aivenator"

	LabelAivenOperation = "operation"
	LabelNamespace      = "namespace"
	LabelPool           = "pool"
	LabelResourceType   = "resource_type"
	LabelStatus         = "status"
	LabelSyncState      = "synchronization_state"
)

var (
	TopicsProcessed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name:      "aiven_applications_processed",
		Namespace: Namespace,
		Help:      "number of applications synchronized with aiven",
	}, []string{LabelSyncState, LabelPool})

	ServiceUsers = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name:      "service_users",
		Namespace: Namespace,
		Help:      "number of service users",
	}, []string{LabelPool})

	AivenLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:      "aiven_latency",
		Namespace: Namespace,
		Help:      "latency in aiven api operations",
		Buckets:   prometheus.LinearBuckets(0.05, 0.05, 100),
	}, []string{LabelAivenOperation, LabelStatus, LabelPool})

	KubernetesResourcesWritten = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name:      "kubernetes_resources_written",
		Namespace: Namespace,
		Help:      "number of kubernetes resources written to the cluster",
	}, []string{LabelNamespace, LabelResourceType})
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
		ServiceUsers,
		TopicsProcessed,
	)
}
