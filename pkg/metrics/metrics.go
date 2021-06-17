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
	ApplicationsProcessed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name:      "aiven_applications_processed",
		Namespace: Namespace,
		Help:      "number of applications synchronized with aiven",
	}, []string{LabelSyncState})

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

	AivenLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:      "aiven_latency",
		Namespace: Namespace,
		Help:      "latency in aiven api operations",
		Buckets:   []float64{.005, .010, .015, .020, .025, .030, .035, .040, .045, .050, .1, .2, .3, .4, .5, 1, 2, 3, 4, 5, 10, 15, 20},
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

	SecretsProtected = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name:      "secrets_protected",
		Namespace: Namespace,
		Help:      "number of secrets found for deletion, but protected by annotation",
	}, []string{LabelNamespace})
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
		SecretsProtected,
	)
}
