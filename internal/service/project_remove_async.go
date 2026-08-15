package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ProjectRemoveReceipt is the bounded durable response for project/remove.
// The existing removal transaction remains the worker operation.
type ProjectRemoveReceipt struct {
	OperationID string               `json:"operation_id"`
	Status      string               `json:"status"`
	Result      *ProjectRemoveResult `json:"result,omitempty"`
	Error       string               `json:"error,omitempty"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

func projectRemoveReceipt(operation durableMutationOperation) ProjectRemoveReceipt {
	receipt := ProjectRemoveReceipt{
		OperationID: operation.OperationID,
		Status:      operation.Status,
		Error:       operation.Error,
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result ProjectRemoveResult
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable project removal result"
		return receipt
	}
	receipt.Result = &result
	return receipt
}

func (s *Service) ProjectRemoveAsync(ctx context.Context, in ProjectRemoveInput) (ProjectRemoveReceipt, error) {
	if err := RequireWorkflowPolicyAuthority(ctx); err != nil {
		return ProjectRemoveReceipt{}, err
	}
	if in.ProjectID == "" {
		return ProjectRemoveReceipt{}, fmt.Errorf("project_id is required")
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "project-remove", in.ProjectID, in)
	if err != nil {
		return ProjectRemoveReceipt{}, err
	}
	return projectRemoveReceipt(operation), nil
}

func (s *Service) ProjectRemoveOperationStatus(ctx context.Context, operationID string) (ProjectRemoveReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return ProjectRemoveReceipt{}, err
	}
	if operation.Kind != "project-remove" {
		return ProjectRemoveReceipt{}, fmt.Errorf("operation is not a project removal mutation")
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return ProjectRemoveReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return projectRemoveReceipt(operation), nil
}
