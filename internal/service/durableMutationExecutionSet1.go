package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) durableMutationExecutionSet1(ctx context.Context, operation durableMutationOperation) (json.RawMessage, error) {
	switch operation.Kind {
	case taskExecutionSubmitKind:
		var input taskExecutionSubmitInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		var result TaskExecutionPublicOutput
		var err error
		if input.Stage == "code" {
			result, err = s.TaskExecutionSubmitCodeForAgent(ctx, operation.ProjectID)
		} else if input.Stage == "rebase" {
			result, err = s.TaskExecutionSubmitRebaseForAgent(ctx, operation.ProjectID)
		} else {
			err = fmt.Errorf("unsupported Task submission stage %q", input.Stage)
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "task-execution-integrate":
		var input TaskExecutionIntegrateInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.TaskExecutionIntegrate(ctx, input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "task-execution-test":
		var input TaskExecutionTestInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.taskExecutionTestRun(ctx, input, operation.CapturedState)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "task-authoring-update":
		var input TaskAuthoringUpdateInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		if s.Durability != nil {
			task, result, err := s.taskAuthoringUpdateShared(ctx, operation.OperationID, input)
			if err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"task": task, "operation": result})
		}
		// The operation marker makes a retry after a process crash safe: if the
		// Hub write committed before the receipt did, the durable Task itself
		// proves that this exact operation already applied.
		if current, err := s.TaskAuthoringRead(ctx, input.ProjectID, input.TaskID); err == nil && current.Metadata != nil && current.Metadata["gateway_operation_id"] == operation.OperationID {
			return json.Marshal(map[string]any{
				"task": current,
				"operation": OperationResult{
					OperationID: operation.OperationID,
					ProjectID:   current.ProjectID,
					TaskID:      current.ID,
					Status:      current.Status,
				},
			})
		}
		task, result, err := s.TaskAuthoringUpdate(ctx, input)
		if err != nil {
			return nil, err
		}
		result.OperationID = operation.OperationID
		return json.Marshal(map[string]any{"task": task, "operation": result})
	case "task-authoring-ready":
		var input TaskAuthoringReadyInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		if s.Durability != nil {
			task, result, err := s.taskAuthoringReadyShared(ctx, operation.OperationID, input)
			if err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"task": task, "operation": result})
		}
		if current, err := s.TaskAuthoringRead(ctx, input.ProjectID, input.TaskID); err == nil && current.Status == model.TaskAuthoringReady && current.ReadySeal != nil && current.ReadySeal.Revision == input.ExpectedRevision && current.ReadySeal.RevisionSHA256 == input.ExpectedRevisionSHA256 && current.ReadySeal.ReadyBy == input.ReadyBy {
			return json.Marshal(map[string]any{
				"task": current,
				"operation": OperationResult{
					OperationID: operation.OperationID,
					ProjectID:   current.ProjectID,
					TaskID:      current.ID,
					Status:      current.Status,
				},
			})
		}
		task, result, err := s.TaskAuthoringReady(ctx, input)
		if err != nil {
			return nil, err
		}
		result.OperationID = operation.OperationID
		return json.Marshal(map[string]any{"task": task, "operation": result})
	}
	return nil, nil
}
