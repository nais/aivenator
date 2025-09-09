package opensearch

import (
	"context"

	"github.com/aiven/aiven-go-client/v2"
)

type ACLManager interface {
	Update(ctx context.Context, project, service string, req aiven.OpenSearchACLRequest) (*aiven.OpenSearchACLResponse, error)
	Get(ctx context.Context, project, service string) (*aiven.OpenSearchACLResponse, error)
}
