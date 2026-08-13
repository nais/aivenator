package opensearch

import (
	"context"

	"github.com/aiven/aiven-go-client/v2"
)

// ACLManager reads the live OpenSearch ACL config from the Aiven API.
// Transitional: its only remaining use is seeding the OpenSearchACLConfig CR
// with pre-migration entries when the CR is first created. Delete after the
// old-format credentials have drained (one full secret-rotation cycle).
type ACLManager interface {
	Get(ctx context.Context, project, service string) (*aiven.OpenSearchACLResponse, error)
}
