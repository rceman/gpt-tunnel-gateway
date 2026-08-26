package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) registerTrainV2ActionSet4() error {
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/full-proof",
		Description:  "Record full Train proof and move a terminal reviewed Train to ready_for_integration without integrating it.",
		InputSchema:  trainV2FullProofSchema(),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2FullProofInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TrainV2FullProofAsync(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/integrate",
		Description:  "Integrate one fully proved Train v2 lane through strict fast-forward and activation receipts.",
		InputSchema:  trainV2IntegrateSchema(),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2IntegrateInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TrainV2IntegrateAsync(ctx, in)
		},
	}); err != nil {
		return err
	}
	return s.RegisterGenericAction(GenericAction{
		Path:         "train/cutover",
		Description:  "Atomically activate train_v2 authority after bounded migration, runtime and Action Registry proofs.",
		InputSchema:  trainV2CutoverSchema(),
		OutputSchema: trainV2CutoverReceiptOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			if err := s.validateTrainV2ActionRegistry(); err != nil {
				return nil, err
			}
			var in service.TrainV2CutoverInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TrainV2CutoverAsync(ctx, in)
		},
	})
	return nil
}
