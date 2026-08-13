package service

import (
	"context"
	"fmt"
	"os"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) releaseTrainRuntime(ctx context.Context, project config.ProjectConfig, projectID, trainID, branch, head string) error {
	if err := s.Git.RemoveTrainWorktree(ctx, project, s.Config.StateDir, projectID, trainID); err != nil {
		return fmt.Errorf("release Train worktree: %w", err)
	}
	if err := s.Git.DeleteTrainBranch(ctx, project, branch, head); err != nil {
		return fmt.Errorf("release Train lane branch: %w", err)
	}
	if err := os.Remove(trainv2.RuntimePath(s.Config.StateDir, projectID, trainID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("release Train runtime binding: %w", err)
	}
	return nil
}

func (s *Service) completeTrainV2Integration(ctx context.Context, in TrainV2IntegrateInput, revision int, laneHead string, post TaskActivationResult, receipt trainv2.IntegrationReceipt, project config.ProjectConfig, laneBranch string) (trainv2.IntegrationReceipt, OperationResult, error) {
	expected := in.ExpectedHubRevision
	var err error
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, err
		}
	}
	var integrated model.TrainV2
	tx, err := s.Hub.Transact(ctx, expected, "gateway: integrate Train v2 "+in.TrainID, func(worktree string) ([]string, error) {
		var latest model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), &latest); err != nil {
			return nil, err
		}
		if latest.Revision != revision || latest.FullProof == nil || latest.FullProof.CandidateHead != laneHead {
			return nil, fmt.Errorf("Train changed before integration")
		}
		integrated, err = trainv2.MarkIntegrated(latest, laneHead, post.SourceHead, receipt.UpdatedAt)
		if err != nil {
			return nil, err
		}
		trainPath := s.trainV2Path(in.ProjectID, in.TrainID)
		receiptPath := trainV2IntegrationPath(in.ProjectID, in.TrainID)
		if err := hub.WriteJSON(worktree, trainPath, integrated); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, receiptPath, receipt); err != nil {
			return nil, err
		}
		return []string{trainPath, receiptPath}, nil
	})
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	if err := s.releaseTrainRuntime(ctx, project, in.ProjectID, in.TrainID, laneBranch, laneHead); err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	return receipt, OperationResult{Hub: tx, ProjectID: in.ProjectID, Status: receipt.Status}, nil
}

func (s *Service) persistTrainV2(ctx context.Context, projectID, trainID string, revision int, updated model.TrainV2) error {
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return err
	}
	_, err = s.Hub.Transact(ctx, expected, "gateway: persist Train v2 proof "+trainID, func(worktree string) ([]string, error) {
		var current model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(projectID, trainID), &current); err != nil {
			return nil, err
		}
		if current.Revision != revision {
			return nil, fmt.Errorf("Train v2 changed before proof persistence")
		}
		if err := hub.WriteJSON(worktree, s.trainV2Path(projectID, trainID), updated); err != nil {
			return nil, err
		}
		return []string{s.trainV2Path(projectID, trainID)}, nil
	})
	return err
}

func (s *Service) persistTrainV2Reconciliation(ctx context.Context, projectID, trainID string, revision int, updated model.TrainV2, receipt trainv2.IntegrationReceipt) error {
	if err := model.ValidateTrainV2(updated); err != nil {
		return err
	}
	if err := trainv2.ValidateIntegrationReceipt(receipt); err != nil {
		return err
	}
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return err
	}
	_, err = s.Hub.Transact(ctx, expected, "gateway: record Train reconciliation restart "+trainID, func(worktree string) ([]string, error) {
		var current model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(projectID, trainID), &current); err != nil {
			return nil, err
		}
		if current.Revision != revision {
			return nil, fmt.Errorf("Train v2 changed before reconciliation reset persistence")
		}
		if err := hub.WriteJSON(worktree, s.trainV2Path(projectID, trainID), updated); err != nil {
			return nil, err
		}
		receiptPath := trainV2IntegrationPath(projectID, trainID)
		if err := hub.WriteJSON(worktree, receiptPath, receipt); err != nil {
			return nil, err
		}
		return []string{s.trainV2Path(projectID, trainID), receiptPath}, nil
	})
	return err
}

func (s *Service) finishTrainReconciliationRestart(ctx context.Context, projectID, trainID string, receipt trainv2.IntegrationReceipt) (trainv2.IntegrationReceipt, OperationResult, error) {
	project, err := s.projectConfig(projectID)
	if err != nil {
		return receipt, OperationResult{ProjectID: projectID, Status: receipt.Status}, err
	}
	startPath := hub.ProtocolRoot + "/projects/" + projectID + "/train-v2-starts/" + trainID + ".json"
	var start model.TrainV2StartRecord
	if err := s.Hub.ReadJSON(ctx, startPath, &start); err != nil {
		return receipt, OperationResult{ProjectID: projectID, Status: receipt.Status}, fmt.Errorf("read reconciled Train start: %w", err)
	}
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, projectID, trainID)
	if err != nil {
		return receipt, OperationResult{ProjectID: projectID, Status: receipt.Status}, fmt.Errorf("reconciled Train runtime is unavailable: %w", err)
	}
	lane := project
	lane.Root = runtime.WorktreePath
	target := receipt.LaneHead
	if receipt.Status == "reconciliation_complete" && receipt.TargetBefore != "" {
		target = receipt.TargetBefore
	}
	currentHead, currentBranch, clean, err := s.Git.CurrentHead(ctx, lane)
	if err != nil || !clean || currentBranch != start.LaneBranch {
		return receipt, OperationResult{ProjectID: projectID, Status: receipt.Status}, fmt.Errorf("reconciled Train lane is not active and clean")
	}
	if currentHead != target {
		if err := s.Git.ResetTrainWorktree(ctx, lane, target); err != nil {
			return receipt, OperationResult{ProjectID: projectID, Status: receipt.Status}, fmt.Errorf("finish local reconciliation reset: %w", err)
		}
	}
	if _, err := trainv2.RetireRuntimeForRestart(s.Config.StateDir, projectID, trainID, start.CurrentAttemptNumber); err != nil {
		return receipt, OperationResult{ProjectID: projectID, Status: receipt.Status}, fmt.Errorf("finish local execution retirement: %w", err)
	}
	return receipt, OperationResult{ProjectID: projectID, Status: receipt.Status}, fmt.Errorf("Train reconciliation requires restart from the refreshed target; it is not integrated")
}
