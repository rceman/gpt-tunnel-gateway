package service

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

type TrainV2IntegrateResult = trainv2.IntegrationReceipt

func trainV2IntegrationPath(projectID, trainID string) string {
	return "gpt-tunnel/v1/projects/" + projectID + "/trains-v2/" + trainID + ".integration.json"
}

func (s *Service) readTrainV2IntegrationReceipt(ctx context.Context, projectID, trainID string) (trainv2.IntegrationReceipt, error) {
	var receipt trainv2.IntegrationReceipt
	if err := s.Hub.ReadJSON(ctx, trainV2IntegrationPath(projectID, trainID), &receipt); err != nil {
		return trainv2.IntegrationReceipt{}, err
	}
	if err := trainv2.ValidateIntegrationReceipt(receipt); err != nil {
		return trainv2.IntegrationReceipt{}, err
	}
	return receipt, nil
}

func (s *Service) writeTrainV2IntegrationReceipt(ctx context.Context, receipt trainv2.IntegrationReceipt) error {
	if err := trainv2.ValidateIntegrationReceipt(receipt); err != nil {
		return err
	}
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return err
	}
	_, err = s.Hub.Transact(ctx, expected, "gateway: record train v2 reconciliation "+receipt.TrainID, func(worktree string) ([]string, error) {
		path := trainV2IntegrationPath(receipt.ProjectID, receipt.TrainID)
		if err := hub.WriteJSON(worktree, path, receipt); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	return err
}
