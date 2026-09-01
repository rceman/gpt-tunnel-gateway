package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) TrainV2Create(ctx context.Context, in TrainV2CreateInput) (model.TrainV2, OperationResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	if in.CreatedBy == "" || strings.ContainsAny(in.CreatedBy, "\x00\r\n") {
		return model.TrainV2{}, OperationResult{}, fmt.Errorf("created_by is required")
	}
	if err := trainv2.ValidateTaskIDs(in.TaskIDs); err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	if _, err := s.ProjectRead(ctx, in.ProjectID); err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, in.ProjectID)
	if err != nil {
		return model.TrainV2{}, OperationResult{}, fmt.Errorf("read project identifiers: %w", err)
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return model.TrainV2{}, OperationResult{}, err
		}
	}
	now := s.durableNow()
	var created model.TrainV2
	tx, err := s.Hub.Transact(ctx, expected, "gateway: create train v2", func(worktree string) ([]string, error) {
		trainID, err := nextTrainV2ID(worktree, s.trainV2Root(in.ProjectID), identifiers.ProjectCode)
		if err != nil {
			return nil, err
		}
		tasks, err := s.trainV2AdmissionTasks(worktree, in.ProjectID, in.TaskIDs)
		if err != nil {
			return nil, err
		}
		tasks, err = bindTrainV2AdmissionTasks(tasks, now)
		if err != nil {
			return nil, err
		}
		created, err = trainv2.New(in.ProjectID, trainID, in.CreatedBy, tasks, now)
		if err != nil {
			return nil, err
		}
		path := s.trainV2Path(in.ProjectID, trainID)
		if _, statErr := os.Lstat(filepath.Join(worktree, filepath.FromSlash(path))); statErr == nil {
			return nil, fmt.Errorf("train v2 already exists")
		} else if !os.IsNotExist(statErr) {
			return nil, statErr
		}
		if err := hub.WriteJSON(worktree, path, created); err != nil {
			return nil, err
		}
		changed := []string{path}
		for _, task := range tasks {
			taskPath := s.taskAuthoringPath(in.ProjectID, task.ID)
			if err := hub.WriteJSON(worktree, taskPath, task); err != nil {
				return nil, err
			}
			changed = append(changed, taskPath)
		}
		return changed, nil
	})
	if err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	return created, OperationResult{
		Hub:       tx,
		ProjectID: in.ProjectID,
		Status:    created.Status,
	}, nil
}
func (s *Service) TrainV2Add(ctx context.Context, in TrainV2AddInput) (model.TrainV2, OperationResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	if in.AddedBy == "" || strings.ContainsAny(in.AddedBy, "\x00\r\n") || in.ExpectedRevision < 1 {
		return model.TrainV2{}, OperationResult{}, fmt.Errorf("expected_revision and added_by are required")
	}
	if err := trainv2.ValidateTaskIDs(in.TaskIDs); err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	current, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	if current.Revision != in.ExpectedRevision {
		return model.TrainV2{}, OperationResult{}, trainRevisionStatusConflict("precondition", "revision", in.ExpectedRevision, current.Revision, current.Status)
	}
	if !trainV2AddableStatus(current.Status) {
		return model.TrainV2{}, OperationResult{}, trainRevisionStatusConflict("precondition", "status", in.ExpectedRevision, current.Revision, current.Status)
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return model.TrainV2{}, OperationResult{}, err
		}
	}
	now := s.durableNow()
	var updated model.TrainV2
	tx, err := s.Hub.Transact(ctx, expected, "gateway: add tasks to train v2 "+in.TrainID, func(worktree string) ([]string, error) {
		var latest model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), &latest); err != nil {
			return nil, err
		}
		if latest.Revision != in.ExpectedRevision {
			return nil, trainRevisionStatusConflict("transaction", "revision", in.ExpectedRevision, latest.Revision, latest.Status)
		}
		if !trainV2AddableStatus(latest.Status) {
			return nil, trainRevisionStatusConflict("transaction", "status", in.ExpectedRevision, latest.Revision, latest.Status)
		}
		if latest.Status == model.TrainV2ReadyForIntegration {
			var receipt trainv2.IntegrationReceipt
			receiptPath := trainV2IntegrationPath(in.ProjectID, in.TrainID)
			err := readWorktreeJSON(worktree, receiptPath, &receipt)
			if err == nil {
				if err := trainv2.ValidateIntegrationReceipt(receipt); err != nil {
					return nil, fmt.Errorf("invalid Train integration receipt: %w", err)
				}
				if receipt.Status == "completed" {
					return nil, trainIntegrationReceiptConflict("receipt", latest.Revision, latest.Status, receipt.Status)
				}
			} else if !os.IsNotExist(err) {
				return nil, fmt.Errorf("read Train integration receipt: %w", err)
			}
		}
		if len(latest.Items)+len(in.TaskIDs) > model.MaxTrainV2Items {
			return nil, fmt.Errorf("train v2 item limit exceeded")
		}
		tasks, err := s.trainV2AdmissionTasks(worktree, in.ProjectID, in.TaskIDs)
		if err != nil {
			return nil, err
		}
		tasks, err = bindTrainV2AdmissionTasks(tasks, now)
		if err != nil {
			return nil, err
		}
		updated, err = trainv2.Append(latest, tasks, now)
		if err != nil {
			return nil, err
		}
		path := s.trainV2Path(in.ProjectID, in.TrainID)
		if err := hub.WriteJSON(worktree, path, updated); err != nil {
			return nil, err
		}
		changed := []string{path}
		for _, task := range tasks {
			taskPath := s.taskAuthoringPath(in.ProjectID, task.ID)
			if err := hub.WriteJSON(worktree, taskPath, task); err != nil {
				return nil, err
			}
			changed = append(changed, taskPath)
		}
		return changed, nil
	})
	if err != nil {
		return model.TrainV2{}, OperationResult{}, err
	}
	return updated, OperationResult{
		Hub:       tx,
		ProjectID: in.ProjectID,
		Status:    updated.Status,
	}, nil
}
