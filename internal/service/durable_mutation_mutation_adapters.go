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
	return s.enqueueTypedDurableMutation(ctx, "task-authoring-ready", in.ProjectID, in)
}
func (s *Service) readDurableMutation(operationID string) (durableMutationOperation, error) {
	if model.ValidateOperationID(operationID) != nil && (!strings.HasPrefix(operationID, "mutation-") || model.ValidateObjectIdentifier(operationID) != nil) {
		return durableMutationOperation{}, fmt.Errorf("invalid durable mutation operation identifier")
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
	if operation.MutationID == "" {
		operation.MutationID = operation.RequestSHA256
	} else if len(operation.MutationID) != sha256.Size*2 {
		return durableMutationOperation{}, fmt.Errorf("invalid durable mutation identity")
	} else if _, err := hex.DecodeString(operation.MutationID); err != nil {
		return durableMutationOperation{}, fmt.Errorf("invalid durable mutation identity: %w", err)
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
	if err := fsutil.WriteJSONAtomic(durableMutationPath(s.Config.StateDir, operation.OperationID), operation, 0o600); err != nil {
		return err
	}
	if model.ValidateOperationID(operation.OperationID) == nil {
		if s.Durability == nil {
			return fmt.Errorf("local durability is unavailable")
		}
		local, err := s.Durability.ReadLocalOperation(context.Background(), operation.OperationID)
		if err != nil {
			return err
		}
		local.Status = operation.Status
		local.ResultPayload = append([]byte(nil), operation.Result...)
		local.Error = operation.Error
		local.RecoveryReason = operation.RecoveryReason
		local.UpdatedAt = operation.UpdatedAt
		if len(operation.Input) > 0 {
			local.AdmissionSessionID = operation.SessionID
			local.AdmissionInputSHA256 = durableMutationInputSHA256(operation.Input)
		}
		if err := s.Durability.UpdateLocalOperation(context.Background(), local); err != nil {
			return err
		}
	}
	return nil
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
	projectCode, err := s.localOperationProjectCode(ctx, in.ProjectID)
	if err != nil {
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
	in.Metadata = &metadata
	input, err = json.Marshal(in)
	if err != nil {
		return durableMutationOperation{}, err
	}
	s.durableMutationMu.Lock()
	defer s.durableMutationMu.Unlock()
	now := time.Now().UTC()
	allocated, err := s.Durability.AllocateLocalOperation(ctx, in.ProjectID, projectCode, digest, "task-authoring-update", now)
	if err != nil {
		return durableMutationOperation{}, err
	}
	operationID := allocated.OperationID
	metadata["gateway_operation_id"] = operationID
	in.Metadata = &metadata
	input, err = json.Marshal(in)
	if err != nil {
		return durableMutationOperation{}, err
	}
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
	now = allocated.CreatedAt
	operation = durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   operationID,
		MutationID:    digest,
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
