package service

import (
	"context"
	"fmt"

	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) resumeTrainV2IntegrationReceipt(ctx context.Context, in TrainV2IntegrateInput, receipt trainv2.IntegrationReceipt) (trainv2.IntegrationReceipt, OperationResult, error, bool) {
	if receipt.Status == "completed" {
		if operation, operationErr := s.readIntegrationOperation(ctx, in.ProjectID, in.TrainID); operationErr == nil && operation.Phase != trainv2.IntegrationPhaseCompleted {
			_, _ = s.advanceIntegrationOperation(ctx, operation, trainv2.IntegrationPhaseCompleted, operation.PostResult)
		}
		project, projectErr := s.projectConfig(in.ProjectID)
		if projectErr != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, projectErr, true
		}
		if _, err := s.trainV2ReadShared(ctx, in.ProjectID, in.TrainID); err != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("read completed Train: %w", err), true
		}
		if cleanupErr := s.releaseTrainRuntime(ctx, project, in.ProjectID, in.TrainID, "train/"+in.TrainID, receipt.LaneHead); cleanupErr != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, cleanupErr, true
		}
		return receipt, OperationResult{
			ProjectID: in.ProjectID,
			Status:    receipt.Status,
		}, nil, true
	}
	if receipt.Status == "reconciliation_blocked" {
		return receipt, OperationResult{
			ProjectID: in.ProjectID,
			Status:    receipt.Status,
		}, fmt.Errorf("Train reconciliation is blocked; bounded Agent correction is required"), true
	}
	if receipt.Status == "reconciliation_complete" || receipt.Status == "reconciliation_requires_restart" {
		result, operation, err := s.finishTrainReconciliationRestart(ctx, in.ProjectID, in.TrainID, receipt)
		return result, operation, err, true
	}
	return trainv2.IntegrationReceipt{}, OperationResult{}, nil, false
}
