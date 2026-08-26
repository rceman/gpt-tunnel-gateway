package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

const migratedIntegrationRecoveryReasonPrefix = "resumed migrated recovery_required integration"

func trainV2IntegrationOperationPath(projectID, trainID string) string {
	return "gpt-tunnel/v1/projects/" + projectID + "/trains-v2/" + trainID + ".integration-operation.json"
}

func trainV2IntegrationOperationHistoryPath(projectID, trainID, operationID string) string {
	return "gpt-tunnel/v1/projects/" + projectID + "/trains-v2/integration-operation-history/" + trainID + "/" + operationID + ".json"
}

func sharedIntegrationOperationID(projectID, trainID string) string {
	return projectID + "\x00" + trainID
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
	if s.Durability == nil {
		return trainv2.IntegrationOperation{}, fmt.Errorf("Shared integration operation authority is unavailable")
	}
	entity, err := s.Durability.ReadSharedEntity(ctx, "integration_operation", sharedIntegrationOperationID(projectID, trainID))
	if err != nil {
		return trainv2.IntegrationOperation{}, err
	}
	var operation trainv2.IntegrationOperation
	if err := json.Unmarshal(entity.Payload, &operation); err != nil {
		return trainv2.IntegrationOperation{}, err
	}
	if err := trainv2.ValidateIntegrationOperation(operation); err != nil {
		return trainv2.IntegrationOperation{}, err
	}
	if operation.ProjectID != projectID || operation.TrainID != trainID {
		return trainv2.IntegrationOperation{}, fmt.Errorf("Shared integration operation identity mismatch")
	}
	return operation, nil
}

func (s *Service) persistIntegrationOperation(ctx context.Context, operation trainv2.IntegrationOperation) error {
	if err := trainv2.ValidateIntegrationOperation(operation); err != nil {
		return err
	}
	if s.Durability == nil {
		return fmt.Errorf("Shared integration operation authority is unavailable")
	}
	payload, err := json.Marshal(operation)
	if err != nil {
		return err
	}
	entityID := sharedIntegrationOperationID(operation.ProjectID, operation.TrainID)
	expectedRevision := int64(0)
	create := true
	if current, readErr := s.Durability.ReadSharedEntity(ctx, "integration_operation", entityID); readErr == nil {
		expectedRevision = current.Revision
		create = false
	} else if !IsNotFound(readErr) {
		return readErr
	}
	digest := sha256.Sum256(payload)
	operationID := "integration-operation-" + hex.EncodeToString(digest[:])
	_, err = s.Durability.CommitSharedMutation(ctx, sqlitestore.SharedMutation{
		OperationID: operationID, EntityType: "integration_operation", EntityID: entityID,
		ExpectedRevision: expectedRevision, Revision: expectedRevision + 1, Kind: "train-integration-operation",
		Payload: payload, CreatedAt: operation.UpdatedAt, Create: create,
	})
	return err
}
