package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
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
		startPath := hub.ProtocolRoot + "/projects/" + in.ProjectID + "/train-v2-starts/" + in.TrainID + ".json"
		var start model.TrainV2StartRecord
		if startErr := s.Hub.ReadJSON(ctx, startPath, &start); startErr != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("read completed Train start: %w", startErr), true
		}
		if cleanupErr := s.releaseTrainRuntime(ctx, project, in.ProjectID, in.TrainID, start.LaneBranch, receipt.LaneHead); cleanupErr != nil {
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
