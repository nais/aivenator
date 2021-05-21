package aiven

const (
	MaxServiceUserNameLength = 64

	ServiceUserAnnotation = "aiven.nais.io/serviceUser"
	PoolAnnotation        = "aiven.nais.io/pool"
)

func DefaultKafkaService(project string) string {
	return project + "-kafka"
}
