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
				}
				workerCancel()
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (s *Service) publishSharedOutboxEntry(ctx context.Context, entry sqlitestore.OutboxEntry) error {
	if entry.EntityType != "task" {
		return fmt.Errorf("unsupported shared outbox entity %q", entry.EntityType)
	}
	var task model.TaskAuthoring
	if err := json.Unmarshal(entry.Payload, &task); err != nil {
		return err
	}
	if err := model.ValidateTaskAuthoring(task); err != nil {
		return err
	}
	current, err := s.TaskAuthoringRead(ctx, task.ProjectID, task.ID)
	if err == nil {
		if current.Revision > task.Revision {
			return fmt.Errorf("Hub task revision is newer than Shared outbox")
		}
		if current.Revision == task.Revision {
			if current.RevisionSHA256 != task.RevisionSHA256 {
				return fmt.Errorf("Hub task revision conflicts with Shared outbox")
			}
			if current.Status == task.Status {
				return nil
			}
		}
	} else if !IsNotFound(err) {
		return err
	}
	path := s.taskAuthoringPath(task.ProjectID, task.ID)
	_, err = s.Hub.Transact(ctx, "", "gateway: publish Shared task "+task.ID, func(worktree string) ([]string, error) {
		var latest model.TaskAuthoring
		if readErr := readWorktreeJSON(worktree, path, &latest); readErr == nil {
			if latest.Revision > task.Revision || (latest.Revision == task.Revision && latest.RevisionSHA256 == task.RevisionSHA256 && latest.Status == task.Status) {
				return nil, fmt.Errorf("Hub task changed while publishing Shared outbox")
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
}
