package aiven

const (
	MaxServiceUserNameLength = 64

	ServiceUserAnnotation = "aiven.kafka.nais.io/serviceUser"
	PoolAnnotation        = "aiven.kafka.nais.io/pool"
)

func DefaultKafkaService(project string) string {
	return project + "-kafka"
}
