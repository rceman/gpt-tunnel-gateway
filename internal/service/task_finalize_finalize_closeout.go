package service

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) advanceFinalizedTask(ctx context.Context, projectID, taskID string, result TrainV2AttemptFinalizeResult) (TrainV2AttemptFinalizeResult, error) {
	lock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "train-"+result.Report.TrainID)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	defer lock.Release()
	return s.advanceFinalizedTaskLocked(ctx, projectID, taskID, result)
}
func (s *Service) advanceFinalizedTaskLocked(ctx context.Context, projectID, taskID string, result TrainV2AttemptFinalizeResult) (TrainV2AttemptFinalizeResult, error) {
	train, err := s.TrainV2Read(ctx, projectID, result.Report.TrainID)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	for _, item := range train.Items {
		if item.TaskID == taskID {
			if item.Position+1 < len(train.Items) && train.Items[item.Position+1].Status == model.TrainV2ItemQueued {
				advanced, err := s.advanceTrainV2Locked(ctx, TrainV2AdvanceInput{
					ProjectID: projectID,
					TrainID:   train.ID,
				}, true)
				if err != nil {
					return TrainV2AttemptFinalizeResult{}, err
				}
				result.NextTaskID = advanced.Record.CurrentTaskID
				return result, nil
			}
			closed, err := s.closeoutTrain(ctx, projectID, train)
			if err != nil {
				return TrainV2AttemptFinalizeResult{}, err
			}
			result.TrainStatus = closed.Status
			break
		}
	}
	return result, nil
}
func (s *Service) closeoutTrain(ctx context.Context, projectID string, train model.TrainV2) (model.TrainV2, error) {
	project, err := s.projectConfig(projectID)
	if err != nil {
		return model.TrainV2{}, err
	}
	startPath := hub.ProtocolRoot + "/projects/" + projectID + "/train-v2-starts/" + train.ID + ".json"
	var start model.TrainV2StartRecord
	if err := s.Hub.ReadJSON(ctx, startPath, &start); err != nil {
		return model.TrainV2{}, err
	}
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, projectID, train.ID)
	if err != nil {
		return model.TrainV2{}, fmt.Errorf("read Train closeout runtime: %w", err)
	}
	project.Root = runtime.WorktreePath
	head, branch, clean, err := s.Git.CurrentHead(ctx, project)
	if err != nil {
		return model.TrainV2{}, err
	}
	if !clean || branch != start.LaneBranch {
		return model.TrainV2{}, fmt.Errorf("Train closeout requires a clean bound worktree")
	}
	for _, item := range train.Items {
		if item.Status != model.TrainV2ItemFinalized || item.Proof != nil {
			continue
		}
		if item.SuccessfulAttemptNumber == 0 || item.SuccessfulAttemptNumber > uint64(len(item.Attempts)) {
			return model.TrainV2{}, fmt.Errorf("Train closeout cannot recover an item without a successful Attempt")
		}
		reportPath := trainV2AttemptReportPath(projectID, train.ID, item.Position, item.SuccessfulAttemptNumber)
		var report model.TrainV2AttemptReport
		if err := s.Hub.ReadJSON(ctx, reportPath, &report); err != nil {
			return model.TrainV2{}, err
		}
		if _, err := s.recoverFinalizedTaskProof(ctx, projectID, train, item, report, reportPath); err != nil {
			return model.TrainV2{}, err
		}
		train, err = s.TrainV2Read(ctx, projectID, train.ID)
		if err != nil {
			return model.TrainV2{}, err
		}
	}
	if train.FullProof != nil {
		if train.FullProof.CandidateHead != head {
			return model.TrainV2{}, fmt.Errorf("Train closeout proof invalidated by exact-head drift")
		}
		for _, item := range train.Items {
			if item.Status != model.TrainV2ItemFinalized || item.SuccessfulAttemptNumber == 0 || item.Proof == nil || item.SuccessfulAttemptNumber > uint64(len(item.Attempts)) {
				return model.TrainV2{}, fmt.Errorf("Train closeout requires every finalized item to have proof")
			}
			reportPath := trainV2AttemptReportPath(projectID, train.ID, item.Position, item.SuccessfulAttemptNumber)
			var report model.TrainV2AttemptReport
			if err := s.Hub.ReadJSON(ctx, reportPath, &report); err != nil {
				return model.TrainV2{}, err
			}
			if err := validateStoredTrainItemProof(report, train, item, reportPath, item.TaskID); err != nil {
				return model.TrainV2{}, err
			}
		}
		return train, nil
	}
	for i, item := range train.Items {
		if item.Status != model.TrainV2ItemFinalized || item.SuccessfulAttemptNumber == 0 || item.SuccessfulAttemptNumber > uint64(len(item.Attempts)) || item.Attempts[item.SuccessfulAttemptNumber-1].Status != model.TrainV2AttemptSucceeded {
			return model.TrainV2{}, fmt.Errorf("Train closeout requires every item to have a successful Attempt")
		}
		if item.Proof == nil {
			return model.TrainV2{}, fmt.Errorf("Train closeout requires every finalized item to have proof")
		}
		reportPath := trainV2AttemptReportPath(projectID, train.ID, item.Position, item.SuccessfulAttemptNumber)
		var report model.TrainV2AttemptReport
		if err := s.Hub.ReadJSON(ctx, reportPath, &report); err != nil {
			return model.TrainV2{}, err
		}
		if err := validateStoredTrainItemProof(report, train, item, reportPath, item.TaskID); err != nil {
			return model.TrainV2{}, err
		}
		if i == len(train.Items)-1 && item.Proof.ImplementationSHA != head {
			return model.TrainV2{}, fmt.Errorf("Train closeout requires the final item proof to compose the exact lane head")
		}
	}
	results, err := s.executeTrainGatesWithScopedFormat(ctx, projectID, project, start.BaseRevision, head)
	if err != nil {
		return model.TrainV2{}, fmt.Errorf("Train closeout gates failed; repair the Train lane and retry: %w", err)
	}
	for _, gate := range results {
		if gate.ExitCode != 0 {
			return model.TrainV2{}, fmt.Errorf("Train closeout gate %s failed; repair the Train lane and retry", gate.ID)
		}
	}
	// executeTrainGatesWithScopedFormat already validates the complete before/after
	// repository snapshot, so no second post-gate Git scan is needed here.
	now := time.Now().UTC()
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return model.TrainV2{}, err
	}
	updated := train
	updated.FullProof = &model.TrainV2FullProof{CandidateHead: head, GateResults: append([]model.CompletionGateResult{}, results...), RecordedAt: now}
	updated.Status = model.TrainV2ReadyForIntegration
	updated.Revision++
	updated.UpdatedAt = now
	if err := model.ValidateTrainV2(updated); err != nil {
		return model.TrainV2{}, err
	}
	if _, err := s.Hub.Transact(ctx, expected, "gateway: closeout Train v2", func(worktree string) ([]string, error) {
		path := s.trainV2Path(projectID, train.ID)
		var latest model.TrainV2
		if err := readWorktreeJSON(worktree, path, &latest); err != nil {
			return nil, err
		}
		if latest.Revision != train.Revision || latest.FullProof != nil || latest.Status != model.TrainV2Running {
			return nil, fmt.Errorf("Train changed before closeout proof publication")
		}
		if err := hub.WriteJSON(worktree, path, updated); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		return model.TrainV2{}, err
	}
	return updated, nil
}
