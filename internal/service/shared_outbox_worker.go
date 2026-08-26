package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/persistence"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func (s *Service) startSharedOutboxWorker() {
	if s.Durability == nil {
		return
	}
	s.sharedOutboxWorkerOnce.Do(func() { go s.sharedOutboxWorker() })
}

func (s *Service) sharedOutboxWorker() {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		entries, err := s.Durability.PendingOutbox(ctx, 32)
		cancel()
		if err == nil {
			for _, entry := range entries {
				workerCtx, workerCancel := s.asyncMutationContext("shared-outbox", entry.ID)
				if err := s.publishSharedOutboxEntry(workerCtx, entry); err == nil {
					_ = s.Durability.MarkOutboxPublished(context.Background(), entry.ID, time.Now().UTC())
				} else {
					_ = s.Durability.MarkOutboxRetry(context.Background(), entry.ID, time.Now().UTC().Add(sharedOutboxRetryDelay(entry.Attempts+1)), err)
				}
				workerCancel()
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func sharedOutboxRetryDelay(attempt int64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 5 {
		attempt = 5
	}
	return time.Duration(1<<uint(attempt-1)) * time.Second
}

func (s *Service) publishSharedOutboxEntry(ctx context.Context, entry sqlitestore.OutboxEntry) error {
	if s.Replica == nil {
		return fmt.Errorf("shared publish persistence is unavailable")
	}
	return s.Replica.PublishShared(ctx, persistence.PublishIntent{
		Kind:      persistence.PublishKind(entry.EntityType),
		EntityID:  entry.EntityID,
		ProjectID: entry.ProjectID,
		Payload:   append([]byte(nil), entry.Payload...),
	})
}
