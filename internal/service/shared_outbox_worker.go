package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
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
	switch entry.EntityType {
	case "task":
		var task model.TaskAuthoring
		if err := json.Unmarshal(entry.Payload, &task); err != nil {
			return err
		}
		if err := model.ValidateTaskAuthoring(task); err != nil {
			return err
		}
		path := s.taskAuthoringPath(task.ProjectID, task.ID)
		_, err := s.Hub.Transact(ctx, "", "gateway: publish Shared task "+task.ID, func(worktree string) ([]string, error) {
			var latest model.TaskAuthoring
			if readErr := readWorktreeJSON(worktree, path, &latest); readErr == nil {
				if latest.Revision > task.Revision {
					return nil, fmt.Errorf("Hub task changed while publishing Shared outbox")
				}
				if latest.Revision == task.Revision && latest.RevisionSHA256 == task.RevisionSHA256 && latest.Status == task.Status {
					return nil, nil
				}
			} else if !IsNotFound(readErr) {
				return nil, readErr
			}
			if err := hub.WriteJSON(worktree, path, task); err != nil {
				return nil, err
			}
			return []string{path}, nil
		})
		return err
	case "adr":
		var adr model.ADR
		if err := json.Unmarshal(entry.Payload, &adr); err != nil {
			return err
		}
		if err := model.ValidateADR(adr); err != nil {
			return err
		}
		path := s.adrPath(adr.ProjectID, adr.ID)
		_, err := s.Hub.Transact(ctx, "", "gateway: publish Shared ADR "+adr.ID, func(worktree string) ([]string, error) {
			var latest model.ADR
			if readErr := readWorktreeJSON(worktree, path, &latest); readErr == nil {
				if latest.ID == adr.ID && latest.CreatedAt.Equal(adr.CreatedAt) {
					return nil, nil
				}
			} else if !IsNotFound(readErr) {
				return nil, readErr
			}
			if err := hub.WriteJSON(worktree, path, adr); err != nil {
				return nil, err
			}
			return []string{path}, nil
		})
		return err
	default:
		return fmt.Errorf("unsupported shared outbox entity %q", entry.EntityType)
	}
}
