package aiven

const (
	MaxServiceUserNameLength = 64
	ServiceUserAnnotation    = "aiven.kafka.nais.io/serviceUser"
)

func DefaultKafkaService(project string) string {
	return project + "-kafka"
}
