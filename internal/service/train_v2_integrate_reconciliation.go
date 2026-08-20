package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) reconcileTrainV2Integration(ctx context.Context, in TrainV2IntegrateInput, train model.TrainV2, start model.TrainV2StartRecord, lane config.ProjectConfig, laneHead, targetHead string) (trainv2.IntegrationReceipt, OperationResult, error, bool) {
	ancestor, err := s.Git.IsAncestor(ctx, lane.Root, targetHead, laneHead)
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err, true
	}
	if ancestor {
		return trainv2.IntegrationReceipt{}, OperationResult{}, nil, false
	}
	commits, logErr := s.Git.LocalLog(ctx, lane.Root, start.BaseRevision, laneHead, s.Config.MaxListItems)
	if logErr != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("read owned Train reconciliation range: %w", logErr), true
	}
	commitIDs := make([]string, 0, len(commits))
	for _, commit := range commits {
		commitIDs = append(commitIDs, commit.SHA)
	}
	reconciledHead, _, replayErr := s.Git.ReplayTrainCommits(ctx, lane, targetHead, commitIDs)
	if replayErr != nil {
		receipt := trainv2.IntegrationReceipt{SchemaVersion: 1, ProjectID: in.ProjectID, TrainID: in.TrainID, BaseRevision: start.BaseRevision, LaneHead: laneHead, TargetBefore: targetHead, Status: "reconciliation_blocked", NextAction: "bounded_agent_correction", Conflict: replayErr.Error(), UpdatedAt: time.Now().UTC()}
		if recordErr := s.writeTrainV2IntegrationReceipt(ctx, receipt); recordErr != nil {
			return receipt, OperationResult{}, recordErr, true
		}
		return receipt, OperationResult{
			ProjectID: in.ProjectID,
			Status:    receipt.Status,
		}, replayErr, true
	}
	updatedTrain, rebindErr := trainv2.ResetImplementationProofsForRestart(train, time.Now().UTC())
	if rebindErr != nil {
		_ = s.Git.ResetTrainWorktree(context.Background(), lane, laneHead)
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("reset Train proof for restart: %w", rebindErr), true
	}
	receipt := trainv2.IntegrationReceipt{SchemaVersion: 1, ProjectID: in.ProjectID, TrainID: in.TrainID, BaseRevision: start.BaseRevision, LaneHead: targetHead, TargetBefore: targetHead, Status: "reconciliation_requires_restart", NextAction: "restart_train_items_from_refreshed_target", Conflict: fmt.Sprintf("discarded replay head %s; prior item proofs and reviews were invalidated", reconciledHead), UpdatedAt: time.Now().UTC()}
	if recordErr := s.persistTrainV2Reconciliation(ctx, in.ProjectID, in.TrainID, train.Revision, updatedTrain, receipt); recordErr != nil {
		if restoreErr := s.Git.ResetTrainWorktree(context.Background(), lane, laneHead); restoreErr != nil {
			return receipt, OperationResult{}, fmt.Errorf("record reconciliation reset: %w; restore original Train lane: %v", recordErr, restoreErr), true
		}
		return receipt, OperationResult{}, recordErr, true
	}
	if resetErr := s.Git.ResetTrainWorktree(ctx, lane, targetHead); resetErr != nil {
		return receipt, OperationResult{
			ProjectID: in.ProjectID,
			Status:    receipt.Status,
		}, fmt.Errorf("Train reconciliation is recorded but local replay reset is pending: %w", resetErr), true
	}
	if _, retireErr := trainv2.RetireRuntimeForRestart(s.Config.StateDir, in.ProjectID, in.TrainID, start.CurrentAttemptNumber); retireErr != nil {
		return receipt, OperationResult{
			ProjectID: in.ProjectID,
			Status:    receipt.Status,
		}, fmt.Errorf("Train reconciliation is recorded but local execution retirement is pending: %w", retireErr), true
	}
	return receipt, OperationResult{
		ProjectID: in.ProjectID,
		Status:    receipt.Status,
	}, fmt.Errorf("Train reconciliation requires restart from the refreshed target; replay was discarded and item proofs require re-execution"), true
}
