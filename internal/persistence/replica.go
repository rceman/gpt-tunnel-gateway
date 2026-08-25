package persistence

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
)

// Replica is the narrow read seam for migration/admin records. Service code
// receives opaque record bytes; repository paths and replica mechanics remain
// in this infrastructure adapter.
type Replica interface {
	ReadFile(context.Context, string) ([]byte, error)
}

type hubReplica struct{ store hub.Store }

func NewHubReplica(store hub.Store) Replica { return hubReplica{store: store} }

func (r hubReplica) ReadFile(ctx context.Context, reference string) ([]byte, error) {
	return r.store.ReadFile(ctx, reference)
}
