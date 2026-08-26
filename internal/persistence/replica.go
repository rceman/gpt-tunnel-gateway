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
	PublishShared(context.Context, PublishIntent) error
}

type PublishKind string

const (
	PublishTask                 PublishKind = "task"
	PublishADR                  PublishKind = "adr"
	PublishProjectConfiguration PublishKind = "project_configuration"
	PublishAgent                PublishKind = "agent"
	PublishTrain                PublishKind = "train"
	PublishIntegrationReceipt   PublishKind = "integration_receipt"
	PublishIntegrationOperation PublishKind = "integration_operation"
	PublishJournal              PublishKind = "journal"
	PublishWatcherGuide         PublishKind = "watcher_guide"
	PublishAttemptReport        PublishKind = "attempt_report"
	PublishAttemptReview        PublishKind = "attempt_review"
)

// PublishIntent is the domain-to-replica boundary for asynchronous Hub
// publication. Paths, transactions, and conflict checks are owned by the
// persistence adapter; callers provide only the typed entity kind, identity,
// and already-committed payload.
type PublishIntent struct {
	Kind      PublishKind
	EntityID  string
	ProjectID string
	Payload   []byte
}

type hubReplica struct{ store hub.Store }

func NewHubReplica(store hub.Store) Replica { return hubReplica{store: store} }

func (r hubReplica) ReadFile(ctx context.Context, reference string) ([]byte, error) {
	return r.store.ReadFile(ctx, reference)
}
