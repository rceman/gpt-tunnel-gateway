package service

import (
	"context"
	"fmt"
	"os"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) releaseTrainRuntime(ctx context.Context, project config.ProjectConfig, projectID, trainID, branch, head string) error {
	removeWorktree := func() error {
		runtime, err := trainv2.ReadRuntime(s.Config.StateDir, projectID, trainID)
		if err == nil && runtime.ProjectCode != "" {
			return s.Git.RemoveTrainWorktreeCompact(ctx, project, s.Config.StateDir, runtime.ProjectCode, trainID)
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		return s.Git.RemoveTrainWorktree(ctx, project, s.Config.StateDir, projectID, trainID)
	}
	if err := removeWorktree(); err != nil {
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
	if s.Durability == nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("Shared integration authority is unavailable")
	}
	latest, err := s.trainV2ReadShared(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	if latest.Revision != revision || latest.FullProof == nil || latest.FullProof.CandidateHead != laneHead {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("Train changed before integration")
	}
	integrated, err := trainv2.MarkIntegrated(latest, laneHead, post.SourceHead, receipt.UpdatedAt)
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	if err := s.commitSharedTrain(ctx, "train-integration-"+in.TrainID, integrated, "train-v2-integrate"); err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	if err := s.writeTrainV2IntegrationReceipt(ctx, receipt); err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	if err := s.releaseTrainRuntime(ctx, project, in.ProjectID, in.TrainID, laneBranch, laneHead); err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	return receipt, OperationResult{
		ProjectID: in.ProjectID,
		Status:    receipt.Status,
	}, nil
}

func (s *Service) persistTrainV2(ctx context.Context, projectID, trainID string, revision int, updated model.TrainV2) error {
	current, err := s.trainV2ReadShared(ctx, projectID, trainID)
	if err != nil {
		return err
	}
	if current.Revision != revision {
		return fmt.Errorf("Train v2 changed before proof persistence")
	}
	return s.commitSharedTrain(ctx, fmt.Sprintf("train-proof-%s-%d", trainID, updated.Revision), updated, "train-v2-proof")
}

func (s *Service) persistTrainV2Reconciliation(ctx context.Context, projectID, trainID string, revision int, updated model.TrainV2, receipt trainv2.IntegrationReceipt) error {
	if err := model.ValidateTrainV2(updated); err != nil {
		return err
	}
	if err := trainv2.ValidateIntegrationReceipt(receipt); err != nil {
		return err
	}
	current, err := s.trainV2ReadShared(ctx, projectID, trainID)
	if err != nil {
		return err
	}
	if current.Revision != revision {
		return fmt.Errorf("Train v2 changed before reconciliation reset persistence")
	}
	if err := s.commitSharedTrain(ctx, fmt.Sprintf("train-reconciliation-%s-%d", trainID, updated.Revision), updated, "train-v2-reconciliation"); err != nil {
		return err
	}
	return s.writeTrainV2IntegrationReceipt(ctx, receipt)
}

func (s *Service) finishTrainReconciliationRestart(ctx context.Context, projectID, trainID string, receipt trainv2.IntegrationReceipt) (trainv2.IntegrationReceipt, OperationResult, error) {
	project, err := s.projectConfig(projectID)
	if err != nil {
		return receipt, OperationResult{
			ProjectID: projectID,
			Status:    receipt.Status,
		}, err
	}
	train, err := s.trainV2ReadShared(ctx, projectID, trainID)
	if err != nil {
		return receipt, OperationResult{
			ProjectID: projectID,
			Status:    receipt.Status,
		}, fmt.Errorf("read reconciled Train: %w", err)
	}
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, projectID, trainID)
	if err != nil {
		return receipt, OperationResult{
			ProjectID: projectID,
			Status:    receipt.Status,
		}, fmt.Errorf("reconciled Train runtime is unavailable: %w", err)
	}
	lane := project
	lane.Root = runtime.WorktreePath
	target := receipt.LaneHead
	if receipt.Status == "reconciliation_complete" && receipt.TargetBefore != "" {
		target = receipt.TargetBefore
	}
	currentHead, currentBranch, clean, err := s.Git.CurrentHead(ctx, lane)
	laneBranch := "train/" + trainID
	if err != nil || !clean || currentBranch != laneBranch {
		return receipt, OperationResult{
			ProjectID: projectID,
			Status:    receipt.Status,
		}, fmt.Errorf("reconciled Train lane is not active and clean")
	}
	if currentHead != target {
		if err := s.Git.ResetTrainWorktree(ctx, lane, target); err != nil {
			return receipt, OperationResult{
				ProjectID: projectID,
				Status:    receipt.Status,
			}, fmt.Errorf("finish local reconciliation reset: %w", err)
		}
	}
	_, attemptNumber, _, found := trainv2.ActiveAttemptIdentity(train)
	if !found {
		return receipt, OperationResult{ProjectID: projectID, Status: receipt.Status}, fmt.Errorf("reconciled Train has no active Attempt")
	}
	if _, err := trainv2.RetireRuntimeForRestart(s.Config.StateDir, projectID, trainID, attemptNumber); err != nil {
		return receipt, OperationResult{
			ProjectID: projectID,
			Status:    receipt.Status,
		}, fmt.Errorf("finish local execution retirement: %w", err)
	}
	return receipt, OperationResult{
		ProjectID: projectID,
		Status:    receipt.Status,
	}, fmt.Errorf("Train reconciliation requires restart from the refreshed target; it is not integrated")
}
