package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
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
	case "project_configuration":
		var configuration model.ProjectConfiguration
		if err := json.Unmarshal(entry.Payload, &configuration); err != nil {
			return err
		}
		return s.publishSharedProjectConfiguration(ctx, configuration)
	case "agent":
		var agent model.Agent
		if err := json.Unmarshal(entry.Payload, &agent); err != nil {
			return err
		}
		if err := model.ValidateAgent(agent); err != nil {
			return err
		}
		path := s.agentPath(agent.ProjectID, agent.AgentID)
		_, err := s.Hub.Transact(ctx, "", "gateway: publish Shared Agent "+agent.ProjectID+"/"+agent.AgentID, func(worktree string) ([]string, error) {
			var latest model.Agent
			if readErr := readWorktreeJSON(worktree, path, &latest); readErr == nil {
				if latest.UpdatedAt.Equal(agent.UpdatedAt) && latest.Enabled == agent.Enabled && latest.Role == agent.Role {
					return nil, nil
				}
			} else if !IsNotFound(readErr) {
				return nil, readErr
			}
			if err := hub.WriteJSON(worktree, path, agent); err != nil {
				return nil, err
			}
			return []string{path}, nil
		})
		return err
	case "train":
		var train model.TrainV2
		if err := json.Unmarshal(entry.Payload, &train); err != nil {
			return err
		}
		if err := model.ValidateTrainV2(train); err != nil {
			return err
		}
		path := s.trainV2Path(train.ProjectID, train.ID)
		_, err := s.Hub.Transact(ctx, "", "gateway: publish Shared Train "+train.ID, func(worktree string) ([]string, error) {
			var latest model.TrainV2
			if readErr := readWorktreeJSON(worktree, path, &latest); readErr == nil {
				if latest.Revision > train.Revision {
					return nil, fmt.Errorf("Hub Train changed while publishing Shared outbox")
				}
				if latest.Revision == train.Revision && bytes.Equal(mustJSON(latest), entry.Payload) {
					return nil, nil
				}
			} else if !IsNotFound(readErr) {
				return nil, readErr
			}
			if err := hub.WriteJSON(worktree, path, train); err != nil {
				return nil, err
			}
			return []string{path}, nil
		})
		return err
	case "integration_receipt":
		var receipt trainv2.IntegrationReceipt
		if err := json.Unmarshal(entry.Payload, &receipt); err != nil {
			return err
		}
		if err := trainv2.ValidateIntegrationReceipt(receipt); err != nil {
			return err
		}
		path := trainV2IntegrationPath(receipt.ProjectID, receipt.TrainID)
		_, err := s.Hub.Transact(ctx, "", "gateway: publish Shared Train integration receipt "+receipt.TrainID, func(worktree string) ([]string, error) {
			var latest trainv2.IntegrationReceipt
			if readErr := readWorktreeJSON(worktree, path, &latest); readErr == nil {
				if latest.UpdatedAt.After(receipt.UpdatedAt) {
					return nil, fmt.Errorf("Hub integration receipt changed while publishing Shared outbox")
				}
				if latest.UpdatedAt.Equal(receipt.UpdatedAt) && bytes.Equal(mustJSON(latest), entry.Payload) {
					return nil, nil
				}
			} else if !IsNotFound(readErr) {
				return nil, readErr
			}
			if err := hub.WriteJSON(worktree, path, receipt); err != nil {
				return nil, err
			}
			return []string{path}, nil
		})
		return err
	case "integration_operation":
		var operation trainv2.IntegrationOperation
		if err := json.Unmarshal(entry.Payload, &operation); err != nil {
			return err
		}
		if err := trainv2.ValidateIntegrationOperation(operation); err != nil {
			return err
		}
		path := trainV2IntegrationOperationPath(operation.ProjectID, operation.TrainID)
		_, err := s.Hub.Transact(ctx, "", "gateway: publish Shared Train integration operation "+operation.OperationID, func(worktree string) ([]string, error) {
			var latest trainv2.IntegrationOperation
			if readErr := readWorktreeJSON(worktree, path, &latest); readErr == nil {
				if latest.UpdatedAt.After(operation.UpdatedAt) {
					return nil, fmt.Errorf("Hub integration operation changed while publishing Shared outbox")
				}
				if latest.UpdatedAt.Equal(operation.UpdatedAt) && bytes.Equal(mustJSON(latest), entry.Payload) {
					return nil, nil
				}
			} else if !IsNotFound(readErr) {
				return nil, readErr
			}
			if err := hub.WriteJSON(worktree, path, operation); err != nil {
				return nil, err
			}
			return []string{path}, nil
		})
		return err
	case "journal":
		var event model.OperatorJournalEvent
		if err := json.Unmarshal(entry.Payload, &event); err != nil {
			return err
		}
		if err := model.ValidateOperatorJournalEvent(event); err != nil {
			return err
		}
		path := s.operatorEventPath(event.ProjectID, event.ID)
		counterPath := s.operatorCounterPath(event.ProjectID)
		_, number, err := model.ParseAnyJournalEventID(event.ID)
		if err != nil {
			return err
		}
		_, err = s.Hub.Transact(ctx, "", "gateway: publish Shared operator journal "+event.ID, func(worktree string) ([]string, error) {
			var latest model.OperatorJournalEvent
			if readErr := readWorktreeJSON(worktree, path, &latest); readErr == nil {
				if bytes.Equal(mustJSON(latest), entry.Payload) {
					return nil, nil
				}
				return nil, fmt.Errorf("Hub operator event changed while publishing Shared outbox")
			} else if !IsNotFound(readErr) {
				return nil, readErr
			}
			if err := hub.WriteJSON(worktree, path, event); err != nil {
				return nil, err
			}
			counter := model.OperatorJournalCounter{SchemaVersion: model.OperatorJournalSchemaVersion, ProjectID: event.ProjectID, NextEventNumber: number + 1}
			if raw, readErr := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(counterPath))); readErr == nil {
				var existing model.OperatorJournalCounter
				if err := decodeStrict(raw, &existing); err != nil {
					return nil, err
				}
				if existing.NextEventNumber > counter.NextEventNumber {
					counter = existing
				}
			} else if !IsNotFound(readErr) {
				return nil, readErr
			}
			if err := hub.WriteJSON(worktree, counterPath, counter); err != nil {
				return nil, err
			}
			return []string{path, counterPath}, nil
		})
		return err
	case "watcher_guide":
		var guide model.WatcherGuide
		if err := json.Unmarshal(entry.Payload, &guide); err != nil {
			return err
		}
		if err := model.ValidateWatcherGuide(guide); err != nil {
			return err
		}
		path := s.watcherGuidePath(guide.ProjectID)
		_, err := s.Hub.Transact(ctx, "", "gateway: publish Shared watcher guide "+guide.ProjectID, func(worktree string) ([]string, error) {
			var latest model.WatcherGuide
			if readErr := readWorktreeJSON(worktree, path, &latest); readErr == nil {
				if latest.Revision > guide.Revision {
					return nil, fmt.Errorf("Hub watcher guide changed while publishing Shared outbox")
				}
				if latest.Revision == guide.Revision && bytes.Equal(mustJSON(latest), entry.Payload) {
					return nil, nil
				}
			} else if !IsNotFound(readErr) {
				return nil, readErr
			}
			if err := hub.WriteJSON(worktree, path, guide); err != nil {
				return nil, err
			}
			return []string{path}, nil
		})
		return err
	default:
		return fmt.Errorf("unsupported shared outbox entity %q", entry.EntityType)
	}
}

func mustJSON(value any) []byte {
	payload, _ := json.Marshal(value)
	return payload
}
