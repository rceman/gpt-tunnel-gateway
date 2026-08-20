package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) validateOrphanTaskCheckpoint(ctx context.Context, project config.ProjectConfig, taskID, startHead, checkpoint string) error {
	if model.ValidateCommitSHA(startHead) != nil || model.ValidateCommitSHA(checkpoint) != nil {
		return fmt.Errorf("orphan Task checkpoint identity is invalid")
	}
	commits, err := s.Git.Log(ctx, project, checkpoint, 1)
	if err != nil || len(commits) != 1 {
		return fmt.Errorf("orphan Task checkpoint provenance is unavailable")
	}
	commit := commits[0]
	if commit.SHA != checkpoint || len(commit.Parents) != 1 || commit.Parents[0] != startHead || commit.Subject != "Task checkpoint "+taskID || commit.AuthorName != taskCheckpointAuthor || commit.AuthorEmail != taskCheckpointAuthorEmail {
		return fmt.Errorf("orphan Task checkpoint provenance does not match the active Attempt")
	}
	tree, err := s.Git.TreeID(ctx, project)
	if err != nil {
		return err
	}
	content, err := s.Git.WorktreeContentID(ctx, project)
	if err != nil || tree != content {
		return fmt.Errorf("orphan Task checkpoint tree does not match the prepared worktree")
	}
	return nil
}
func (s *Service) reuseFinalizedTask(ctx context.Context, projectID, taskID string) (TrainV2AttemptFinalizeResult, bool, error) {
	trains, err := s.TrainV2List(ctx, TrainV2ListInput{
		ProjectID: projectID,
		Limit:     model.MaxTrainV2Items,
	})
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, false, err
	}
	for _, train := range trains.Trains {
		for _, item := range train.Items {
			if item.TaskID != taskID || item.Status != model.TrainV2ItemFinalized || item.SuccessfulAttemptNumber == 0 {
				continue
			}
			path := trainV2AttemptReportPath(projectID, train.ID, item.Position, item.SuccessfulAttemptNumber)
			var report model.TrainV2AttemptReport
			if err := s.Hub.ReadJSON(ctx, path, &report); err != nil {
				return TrainV2AttemptFinalizeResult{}, false, err
			}
			if item.Proof == nil {
				recovered, err := s.recoverFinalizedTaskProof(ctx, projectID, train, item, report, path)
				if err != nil {
					return TrainV2AttemptFinalizeResult{}, false, err
				}
				return TrainV2AttemptFinalizeResult{Report: recovered}, true, nil
			}
			if err := validateStoredTrainItemProof(report, train, item, path, taskID); err != nil {
				return TrainV2AttemptFinalizeResult{}, false, fmt.Errorf("stored Task checkpoint proof is invalid")
			}
			return TrainV2AttemptFinalizeResult{Report: report}, true, nil
		}
	}
	return TrainV2AttemptFinalizeResult{}, false, nil
}
