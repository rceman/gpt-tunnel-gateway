package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

var errSharedOutboxNoop = errors.New("shared outbox publication is already current")

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
				if err := s.publishSharedOutboxEntry(workerCtx, entry); err == nil || errors.Is(err, errSharedOutboxNoop) {
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
	publisher, ok := sharedOutboxPublishers[entry.EntityType]
	if !ok {
		return fmt.Errorf("unsupported shared outbox entity %q", entry.EntityType)
	}
	return publisher(s, ctx, entry)
}

var sharedOutboxPublishers = map[string]func(*Service, context.Context, sqlitestore.OutboxEntry) error{
	"task":                  (*Service).publishSharedTaskOutbox,
	"adr":                   (*Service).publishSharedADROutbox,
	"project_configuration": (*Service).publishSharedProjectConfigurationOutbox,
}

func (s *Service) publishSharedTaskOutbox(ctx context.Context, entry sqlitestore.OutboxEntry) error {
	var task model.TaskAuthoring
	if err := json.Unmarshal(entry.Payload, &task); err != nil {
		return err
	}
	if err := model.ValidateTaskAuthoring(task); err != nil {
		return err
	}
	if entry.EntityType != "task" || entry.EntityID != task.ID || entry.Revision != int64(task.Revision) {
		return fmt.Errorf("shared task outbox entry identity mismatch")
	}
	path := s.taskAuthoringPath(task.ProjectID, task.ID)
	_, err := s.Hub.Transact(ctx, "", "gateway: publish Shared task "+task.ID, func(worktree string) ([]string, error) {
		var latest model.TaskAuthoring
		if readErr := readWorktreeJSON(worktree, path, &latest); readErr == nil {
			if err := model.ValidateTaskAuthoring(latest); err != nil {
				return nil, fmt.Errorf("Hub task %q is malformed authority: %w", task.ID, err)
			}
			if latest.ID != task.ID || latest.ProjectID != task.ProjectID {
				return nil, fmt.Errorf("Hub task identity conflicts at %s", path)
			}
			switch {
			case latest.Revision > task.Revision:
				return nil, errSharedOutboxNoop
			case latest.Revision < task.Revision:
			case latest.RevisionSHA256 != task.RevisionSHA256:
				return nil, fmt.Errorf("Hub task content digest conflicts at revision %d", task.Revision)
			case latest.UpdatedAt.After(task.UpdatedAt):
				return nil, errSharedOutboxNoop
			case task.UpdatedAt.After(latest.UpdatedAt):
			default:
				latestCanonical, latestErr := json.Marshal(latest)
				taskCanonical, taskErr := json.Marshal(task)
				if latestErr != nil || taskErr != nil {
					return nil, fmt.Errorf("cannot compare Hub task payload at revision %d", task.Revision)
				}
				if bytes.Equal(latestCanonical, taskCanonical) {
					return nil, errSharedOutboxNoop
				}
				return nil, fmt.Errorf("equal-time Hub task payload is contradictory at revision %d", task.Revision)
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

func (s *Service) publishSharedADROutbox(ctx context.Context, entry sqlitestore.OutboxEntry) error {
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
			if latest.ID == adr.ID && latest.Revision == adr.Revision && reflect.DeepEqual(latest, adr) {
				return nil, errSharedOutboxNoop
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
}

func (s *Service) publishSharedProjectConfigurationOutbox(ctx context.Context, entry sqlitestore.OutboxEntry) error {
	var configuration model.ProjectConfiguration
	if err := json.Unmarshal(entry.Payload, &configuration); err != nil {
		return err
	}
	return s.publishSharedProjectConfiguration(ctx, configuration)
}
