package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) enqueueTrainV2Integrate(ctx context.Context, in TrainV2IntegrateInput) (durableMutationOperation, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return durableMutationOperation{}, err
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return durableMutationOperation{}, err
	}
	input, err := json.Marshal(in)
	if err != nil {
		return durableMutationOperation{}, err
	}
	sessionID := AgentSessionID(ctx)
	digest := durableMutationDigest("train-v2-integrate", sessionID, input)
	operationID := "mutation-" + digest
	if err := model.ValidateObjectIdentifier(operationID); err != nil {
		return durableMutationOperation{}, err
	}
	s.durableMutationMu.Lock()
	defer s.durableMutationMu.Unlock()
	operation, err := s.readDurableMutation(operationID)
	if err == nil {
		if operation.RequestSHA256 != digest || operation.Kind != "train-v2-integrate" {
			return durableMutationOperation{}, fmt.Errorf("durable mutation identity mismatch")
		}
		if operation.Status == "failed" || operation.Status == "outcome_unknown" {
			operation.Status = "accepted"
			operation.Error = ""
			operation.UpdatedAt = time.Now().UTC()
			if err := s.writeDurableMutation(operation); err != nil {
				return durableMutationOperation{}, err
			}
		}
		s.startDurableMutationWorker()
		s.enqueueDurableMutation(operationID)
		return operation, nil
	}
	if !os.IsNotExist(err) {
		return durableMutationOperation{}, err
	}
	now := time.Now().UTC()
	operation = durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   operationID,
		Kind:          "train-v2-integrate",
		RequestSHA256: digest,
		SessionID:     sessionID,
		ProjectID:     in.ProjectID,
		Input:         input,
		Status:        "accepted",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.writeDurableMutation(operation); err != nil {
		return durableMutationOperation{}, err
	}
	s.startDurableMutationWorker()
	s.enqueueDurableMutation(operationID)
	return operation, nil
}
func (s *Service) enqueueTaskAuthoringReady(ctx context.Context, in TaskAuthoringReadyInput) (durableMutationOperation, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return durableMutationOperation{}, err
	}
	if err := model.ValidateCanonicalTaskID(in.TaskID); err != nil {
		return durableMutationOperation{}, err
	}
	if in.ExpectedRevision < 1 || strings.TrimSpace(in.ReadyBy) == "" || strings.ContainsAny(in.ReadyBy, "\x00\r\n") {
		return durableMutationOperation{}, fmt.Errorf("expected_revision and ready_by are required")
	}
	input, err := json.Marshal(in)
	if err != nil {
		return durableMutationOperation{}, err
	}
	sessionID := AgentSessionID(ctx)
	digest := durableMutationDigest("task-authoring-ready", sessionID, input)
	operationID := "mutation-" + digest
	if err := model.ValidateObjectIdentifier(operationID); err != nil {
		return durableMutationOperation{}, err
	}
	s.durableMutationMu.Lock()
	defer s.durableMutationMu.Unlock()
	operation, err := s.readDurableMutation(operationID)
	if err == nil {
		if operation.RequestSHA256 != digest || operation.Kind != "task-authoring-ready" {
			return durableMutationOperation{}, fmt.Errorf("durable mutation identity mismatch")
		}
		if operation.Status == "failed" || operation.Status == "outcome_unknown" {
			operation.Status = "accepted"
			operation.Error = ""
			operation.UpdatedAt = time.Now().UTC()
			if err := s.writeDurableMutation(operation); err != nil {
				return durableMutationOperation{}, err
			}
		}
		s.startDurableMutationWorker()
		s.enqueueDurableMutation(operationID)
		return operation, nil
	}
	if !os.IsNotExist(err) {
		return durableMutationOperation{}, err
	}
	now := time.Now().UTC()
	operation = durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   operationID,
		Kind:          "task-authoring-ready",
		RequestSHA256: digest,
		SessionID:     sessionID,
		ProjectID:     in.ProjectID,
		Input:         input,
		Status:        "accepted",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.writeDurableMutation(operation); err != nil {
		return durableMutationOperation{}, err
	}
	s.startDurableMutationWorker()
	s.enqueueDurableMutation(operationID)
	return operation, nil
}
func (s *Service) readDurableMutation(operationID string) (durableMutationOperation, error) {
	if err := model.ValidateObjectIdentifier(operationID); err != nil {
		return durableMutationOperation{}, err
	}
	var operation durableMutationOperation
	if err := fsutil.ReadJSONBounded(durableMutationPath(s.Config.StateDir, operationID), 1<<20, &operation); err != nil {
		return durableMutationOperation{}, err
	}
	if operation.SchemaVersion != durableMutationSchemaVersion || operation.OperationID != operationID || operation.Kind == "" {
		return durableMutationOperation{}, fmt.Errorf("invalid durable mutation operation")
	}
	if len(operation.RequestSHA256) != sha256.Size*2 {
		return durableMutationOperation{}, fmt.Errorf("invalid durable mutation request digest")
	}
	if _, err := hex.DecodeString(operation.RequestSHA256); err != nil {
		return durableMutationOperation{}, fmt.Errorf("invalid durable mutation request digest: %w", err)
	}
	switch operation.Status {
	case "accepted", "running", "completed", "failed", "outcome_unknown":
	default:
		return durableMutationOperation{}, fmt.Errorf("invalid durable mutation status %q", operation.Status)
	}
	if len(operation.Input) == 0 || operation.ProjectID == "" {
		return durableMutationOperation{}, fmt.Errorf("invalid durable mutation payload")
	}
	return operation, nil
}
func (s *Service) writeDurableMutation(operation durableMutationOperation) error {
	return fsutil.WriteJSONAtomic(durableMutationPath(s.Config.StateDir, operation.OperationID), operation, 0o600)
}
func (s *Service) enqueueTaskAuthoringUpdate(ctx context.Context, in TaskAuthoringUpdateInput) (durableMutationOperation, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return durableMutationOperation{}, err
	}
	if err := model.ValidateCanonicalTaskID(in.TaskID); err != nil {
		return durableMutationOperation{}, err
	}
	if in.ExpectedRevision < 1 || strings.TrimSpace(in.UpdatedBy) == "" || strings.ContainsAny(in.UpdatedBy, "\x00\r\n") {
		return durableMutationOperation{}, fmt.Errorf("expected_revision and updated_by are required")
	}
	input, err := json.Marshal(in)
	if err != nil {
		return durableMutationOperation{}, err
	}
	sessionID := AgentSessionID(ctx)
	digest := durableMutationDigest("task-authoring-update", sessionID, input)
	operationID := "mutation-" + digest
	if err := model.ValidateObjectIdentifier(operationID); err != nil {
		return durableMutationOperation{}, err
	}
	if in.Metadata != nil && (*in.Metadata)["gateway_operation_id"] != "" {
		return durableMutationOperation{}, fmt.Errorf("metadata gateway_operation_id is server-owned")
	}
	metadata := make(map[string]string, lenValue(in.Metadata)+1)
	if in.Metadata != nil {
		for key, value := range *in.Metadata {
			metadata[key] = value
		}
	}
	metadata["gateway_operation_id"] = operationID
	in.Metadata = &metadata
	input, err = json.Marshal(in)
	if err != nil {
		return durableMutationOperation{}, err
	}
	s.durableMutationMu.Lock()
	defer s.durableMutationMu.Unlock()
	operation, err := s.readDurableMutation(operationID)
	if err == nil {
		if operation.RequestSHA256 != digest || operation.Kind != "task-authoring-update" {
			return durableMutationOperation{}, fmt.Errorf("durable mutation identity mismatch")
		}
		if operation.Status == "failed" || operation.Status == "outcome_unknown" {
			operation.Status = "accepted"
			operation.Error = ""
			operation.UpdatedAt = time.Now().UTC()
			if err := s.writeDurableMutation(operation); err != nil {
				return durableMutationOperation{}, err
			}
		}
		s.startDurableMutationWorker()
		s.enqueueDurableMutation(operationID)
		return operation, nil
	}
	if !os.IsNotExist(err) {
		return durableMutationOperation{}, err
	}
	now := time.Now().UTC()
	operation = durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   operationID,
		Kind:          "task-authoring-update",
		RequestSHA256: digest,
		SessionID:     sessionID,
		ProjectID:     in.ProjectID,
		Input:         input,
		Status:        "accepted",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.writeDurableMutation(operation); err != nil {
		return durableMutationOperation{}, err
	}
	s.startDurableMutationWorker()
	s.enqueueDurableMutation(operationID)
	return operation, nil
}
func lenValue(values *map[string]string) int {
	if values == nil {
		return 0
	}
	return len(*values)
}
