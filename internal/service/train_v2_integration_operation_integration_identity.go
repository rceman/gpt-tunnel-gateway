package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

const migratedIntegrationRecoveryReasonPrefix = "resumed migrated recovery_required integration"

func trainV2IntegrationOperationPath(projectID, trainID string) string {
	return "gpt-tunnel/v1/projects/" + projectID + "/trains-v2/" + trainID + ".integration-operation.json"
}
func trainV2IntegrationOperationHistoryPath(projectID, trainID, operationID string) string {
	return "gpt-tunnel/v1/projects/" + projectID + "/trains-v2/integration-operation-history/" + trainID + "/" + operationID + ".json"
}
func integrationRequestDigest(in TrainV2IntegrateInput, source, branch, target string) (string, string, error) {
	payload, err := json.Marshal(struct {
		ProjectID, TrainID, Source, Branch, Target string
	}{in.ProjectID, in.TrainID, source, branch, target})
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256(payload)
	hexDigest := hex.EncodeToString(digest[:])
	return hexDigest, "GTW-INTEGRATE-" + hexDigest[:24], nil
}
func (s *Service) readIntegrationOperation(ctx context.Context, projectID, trainID string) (trainv2.IntegrationOperation, error) {
	var operation trainv2.IntegrationOperation
	if err := s.Hub.ReadJSON(ctx, trainV2IntegrationOperationPath(projectID, trainID), &operation); err != nil {
		return trainv2.IntegrationOperation{}, err
	}
	if err := trainv2.ValidateIntegrationOperation(operation); err != nil {
		return trainv2.IntegrationOperation{}, err
	}
	return operation, nil
}
func (s *Service) persistIntegrationOperation(ctx context.Context, operation trainv2.IntegrationOperation) error {
	if err := trainv2.ValidateIntegrationOperation(operation); err != nil {
		return err
	}
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return err
	}
	_, err = s.Hub.Transact(ctx, expected, "gateway: persist Train integration operation "+operation.OperationID, func(worktree string) ([]string, error) {
		path := trainV2IntegrationOperationPath(operation.ProjectID, operation.TrainID)
		if err := hub.WriteJSON(worktree, path, operation); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	return err
}
