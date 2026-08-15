package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Server) ensureTrainV2Actions() {
	s.trainV2Actions.Do(func() {
		s.trainV2ActionErr = s.registerTrainV2Actions()
	})
	if s.trainV2ActionErr != nil {
		panic(s.trainV2ActionErr)
	}
}

func (s *Server) validateTrainV2ActionRegistry() error {
	registered := make([]string, 0, len(s.genericActions))
	for path := range s.genericActions {
		registered = append(registered, path)
	}
	return trainv2.ValidateActionRegistry(trainv2.RequiredCutoverActions, registered)
}

func (s *Server) registerTrainV2Actions() error {
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/create",
		Description:  "Create a non-running ordered train_v2 admission record.",
		InputSchema:  trainV2CreateSchema(),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  false,
		},
		AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2CreateInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			train, operation, err := s.Service.TrainV2Create(ctx, in)
			if err != nil {
				return nil, err
			}
			return map[string]any{"train": train, "operation": operation}, nil
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/add",
		Description:  "Append ready Tasks to the unstarted tail of a train_v2.",
		InputSchema:  trainV2AddSchema(),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  false,
		},
		AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2AddInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			train, operation, err := s.Service.TrainV2Add(ctx, in)
			if err != nil {
				return nil, err
			}
			return map[string]any{"train": train, "operation": operation}, nil
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/read",
		Description:  "Read one train_v2 admission record.",
		InputSchema:  trainV2ReadSchema(),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				TrainID   string `json:"train_id"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TrainV2Read(ctx, in.ProjectID, in.TrainID)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/list",
		Description:  "List bounded train_v2 admission records.",
		InputSchema:  trainV2ListSchema(),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2ListInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TrainV2List(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/start",
		Description:  "Start one server-owned Train v2 execution lane.",
		InputSchema:  trainV2StartSchema(),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2StartInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TrainV2StartAsync(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:             "train/start_status",
		Description:      "Read the durable receipt for a Train start initiation.",
		InputSchema:      obj(map[string]any{"operation_id": str("Durable Train start operation identifier.")}, "operation_id"),
		OutputSchema:     trainV2OutputSchema(),
		Annotations:      ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				OperationID string `json:"operation_id"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			return s.Service.TrainV2StartOperationStatus(ctx, input.OperationID)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/advance",
		Description:  "Start the next queued TrainItem Attempt without creating a global Run.",
		InputSchema:  trainV2AdvanceSchema(),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2AdvanceInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TrainV2AdvanceAsync(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:             "train/advance_status",
		Description:      "Read the durable receipt for a Train advance initiation.",
		InputSchema:      obj(map[string]any{"operation_id": str("Durable Train advance operation identifier.")}, "operation_id"),
		OutputSchema:     trainV2OutputSchema(),
		Annotations:      ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				OperationID string `json:"operation_id"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			return s.Service.TrainV2AdvanceOperationStatus(ctx, input.OperationID)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/attempt-finalize",
		Description:  "Finalize one exact TrainItem Attempt without creating a global Run.",
		InputSchema:  trainV2AttemptFinalizeSchema(),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2AttemptFinalizeInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TrainV2AttemptFinalize(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/attempt-review",
		Description:  "Publish review for one exact successful TrainItem Attempt.",
		InputSchema:  trainV2AttemptReviewSchema(),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2AttemptReviewInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TrainV2AttemptReview(ctx, in)
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
	return s.RegisterGenericAction(GenericAction{
		Path:         "train/cutover",
		Description:  "Atomically activate train_v2 authority after bounded migration, runtime and Action Registry proofs.",
		InputSchema:  trainV2CutoverSchema(),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			if err := s.validateTrainV2ActionRegistry(); err != nil {
				return nil, err
			}
			var in service.TrainV2CutoverInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			receipt, operation, err := s.Service.TrainV2Cutover(ctx, in)
			return map[string]any{"receipt": receipt, "operation": operation}, err
		},
	})
}
