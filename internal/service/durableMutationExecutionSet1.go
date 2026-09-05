package service

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) durableMutationExecutionSet1(ctx context.Context, operation durableMutationOperation) (json.RawMessage, error) {
	switch operation.Kind {
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
	case "train-v2-integrate":
		var input TrainV2IntegrateInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		receipt, result, err := s.TrainV2Integrate(ctx, input)
		if err != nil {
			return nil, err
		}
		result.OperationID = operation.OperationID
		return json.Marshal(map[string]any{"receipt": receipt, "operation": result})
	case "train-v2-full-proof":
		var input TrainV2FullProofInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.TrainV2FullProof(authority.WithPlanner(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "train-v2-review-backfill":
		var input TrainV2ReviewBackfillInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.TrainV2ReviewBackfill(authority.WithPlanner(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "train-v2-start":
		var input TrainV2StartInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.TrainV2Start(ctx, input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"result": result})
	case "train-v2-advance":
		var input TrainV2AdvanceInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.TrainV2Advance(ctx, input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"result": result})
	case "train-v2-correction-start":
		var input TrainV2CorrectionStartInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.TrainV2CorrectionStart(ctx, input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"result": result})
	}
	return nil, nil
}
