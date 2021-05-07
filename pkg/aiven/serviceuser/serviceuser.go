package serviceuser

import "github.com/aiven/aiven-go-client"

type Interface interface {
	Create(project, service string, req aiven.CreateServiceUserRequest) (*aiven.ServiceUser, error)
	List(project, serviceName string) ([]*aiven.ServiceUser, error)
	Delete(project, service, user string) error
}
