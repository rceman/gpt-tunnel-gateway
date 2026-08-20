package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
)

func (s *Service) durableMutationExecutionSet4(ctx context.Context, operation durableMutationOperation) (json.RawMessage, error) {
	switch operation.Kind {
	case "train-attempt-finalize":
		var input TrainV2AttemptFinalizeInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.TrainV2AttemptFinalize(ctx, input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "train-v2-create":
		var input TrainV2CreateInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		train, result, err := s.TrainV2Create(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"train": train, "operation": result})
	case "train-v2-add":
		var input TrainV2AddInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		train, result, err := s.TrainV2Add(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"train": train, "operation": result})
	case "train-v2-cutover":
		var input TrainV2CutoverInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		receipt, result, err := s.TrainV2Cutover(authority.WithPlannerOrDelivery(ctx), input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"receipt": receipt, "operation": result})
	case "train-attempt-review":
		var input TrainV2AttemptReviewInput
		if err := json.Unmarshal(operation.Input, &input); err != nil {
			return nil, err
		}
		result, err := s.TrainV2AttemptReview(ctx, input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	default:
		return nil, fmt.Errorf("unsupported durable mutation kind %q", operation.Kind)
	}
	return nil, nil
}
