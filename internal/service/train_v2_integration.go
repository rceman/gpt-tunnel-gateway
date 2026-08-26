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

type TrainV2IntegrateResult = trainv2.IntegrationReceipt

func trainV2IntegrationPath(projectID, trainID string) string {
	return "gpt-tunnel/v1/projects/" + projectID + "/trains-v2/" + trainID + ".integration.json"
}

func (s *Service) readTrainV2IntegrationReceipt(ctx context.Context, projectID, trainID string) (trainv2.IntegrationReceipt, error) {
	if s.Durability == nil {
		return trainv2.IntegrationReceipt{}, fmt.Errorf("Shared integration authority is unavailable")
	}
	entity, err := s.Durability.ReadSharedEntity(ctx, "integration_receipt", sqlitestore.SharedIntegrationReceiptID(projectID, trainID))
	if err != nil {
		return trainv2.IntegrationReceipt{}, err
	}
	var receipt trainv2.IntegrationReceipt
	if err := json.Unmarshal(entity.Payload, &receipt); err != nil {
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
	if s.Durability == nil {
		return fmt.Errorf("Shared integration authority is unavailable")
	}
	payload, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	id := sqlitestore.SharedIntegrationReceiptID(receipt.ProjectID, receipt.TrainID)
	expected := int64(0)
	create := true
	if current, readErr := s.Durability.ReadSharedEntity(ctx, "integration_receipt", id); readErr == nil {
		expected = current.Revision
		create = false
		if string(current.Payload) == string(payload) {
			return nil
		}
	} else if !IsNotFound(readErr) {
		return readErr
	}
	digest := sha256.Sum256(payload)
	operationID := "train-integration-receipt-" + hex.EncodeToString(digest[:])
	_, err = s.Durability.CommitSharedMutation(ctx, sqlitestore.SharedMutation{
		OperationID:      operationID,
		EntityType:       "integration_receipt",
		EntityID:         id,
		ExpectedRevision: expected,
		Revision:         expected + 1,
		Kind:             "train-v2-integration-receipt",
		Payload:          payload,
		CreatedAt:        receipt.UpdatedAt,
		Create:           create,
	})
	return err
}
