package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) registerTrainV2ActionSet4() error {
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/review-backfill_status",
		Description:  "Read the durable receipt for Train review backfill.",
		InputSchema:  obj(map[string]any{"operation_id": str("Durable Train review backfill operation identifier.")}, "operation_id"),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				OperationID string `json:"operation_id"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			return s.Service.TrainV2ReviewBackfillOperationStatus(ctx, input.OperationID)
		},
	}); err != nil {
		return err
	}
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
		Path:         "train/full-proof_status",
		Description:  "Read the durable receipt for a Train full-proof transition.",
		InputSchema:  obj(map[string]any{"operation_id": str("Durable Train full-proof operation identifier.")}, "operation_id"),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				OperationID string `json:"operation_id"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			return s.Service.TrainV2FullProofOperationStatus(ctx, input.OperationID)
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
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/integrate_status",
		Description:  "Read the durable initiation receipt and existing Train integration phase.",
		InputSchema:  obj(map[string]any{"operation_id": str("Durable train/integrate operation identifier.")}, "operation_id"),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				OperationID string `json:"operation_id"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			return s.Service.TrainV2IntegrateOperationStatus(ctx, input.OperationID)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/cutover_status",
		Description:  "Read the durable receipt for an asynchronous Train cutover.",
		InputSchema:  obj(map[string]any{"operation_id": str("Durable Train cutover operation identifier.")}, "operation_id"),
		OutputSchema: trainV2CutoverReceiptOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				OperationID string `json:"operation_id"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			return s.Service.TrainV2CutoverOperationStatus(ctx, input.OperationID)
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
