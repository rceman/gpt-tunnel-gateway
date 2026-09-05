package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// ProjectConfigurationMutationReceipt is the bounded durable response for
// project/update. The existing configuration transaction remains the worker.
type ProjectConfigurationMutationReceipt struct {
	OperationID   string                      `json:"operation_id"`
	Status        string                      `json:"status"`
	Configuration *model.ProjectConfiguration `json:"configuration,omitempty"`
	Operation     OperationResult             `json:"operation,omitempty"`
	Error         string                      `json:"error,omitempty"`
	CreatedAt     time.Time                   `json:"created_at"`
	UpdatedAt     time.Time                   `json:"updated_at"`
}

func projectConfigurationMutationReceipt(operation durableMutationOperation) ProjectConfigurationMutationReceipt {
	receipt := ProjectConfigurationMutationReceipt{
		OperationID: operation.OperationID,
		Status:      operation.Status,
		Error:       operation.Error,
		CreatedAt:   operation.CreatedAt,
		UpdatedAt:   operation.UpdatedAt,
	}
	receipt.Operation.Hub.Paths = []string{}
	if operation.Status != "completed" || len(operation.Result) == 0 {
		return receipt
	}
	var result struct {
		Configuration model.ProjectConfiguration `json:"configuration"`
		Operation     OperationResult            `json:"operation"`
	}
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		receipt.Status = "failed"
		receipt.Error = "invalid durable project configuration result"
		return receipt
	}
	receipt.Configuration = &result.Configuration
	receipt.Operation = result.Operation
	if receipt.Operation.Hub.Paths == nil {
		receipt.Operation.Hub.Paths = []string{}
	}
	return receipt
}

func (s *Service) ProjectConfigurationUpdateAsync(ctx context.Context, in ProjectConfigurationUpdateInput) (ProjectConfigurationMutationReceipt, error) {
	if err := RequireWorkflowPolicyAuthority(ctx); err != nil {
		return ProjectConfigurationMutationReceipt{}, err
	}
	if in.ProjectID == "" {
		return ProjectConfigurationMutationReceipt{}, fmt.Errorf("project_id is required")
	}
	operation, err := s.enqueueTypedDurableMutation(ctx, "project-configuration-update", in.ProjectID, in)
	if err != nil {
		return ProjectConfigurationMutationReceipt{}, err
	}
	return projectConfigurationMutationReceipt(operation), nil
}

func (s *Service) ProjectConfigurationUpdateOperationStatus(ctx context.Context, operationID string) (ProjectConfigurationMutationReceipt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return ProjectConfigurationMutationReceipt{}, err
	}
	if operation.Kind != "project-configuration-update" {
		return ProjectConfigurationMutationReceipt{}, fmt.Errorf("operation is not a project configuration mutation")
	}
	if sessionID := AgentSessionID(ctx); sessionID != "" && operation.SessionID != sessionID {
		return ProjectConfigurationMutationReceipt{}, fmt.Errorf("durable mutation session mismatch")
	}
	return projectConfigurationMutationReceipt(operation), nil
}

func projectConfigurationMutationContext(ctx context.Context) context.Context {
	return authority.WithPlanner(ctx)
}
