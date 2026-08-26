package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func (s *Server) registerTrainV2ActionSet1() error {
	if err := s.RegisterGenericAction(GenericAction{
		Path:             "train/abandon",
		Description:      "Abort one active Train Attempt and retire the Train with a server-owned reason.",
		InputSchema:      trainV2AbandonSchema(),
		OutputSchema:     trainV2OutputSchema(),
		Annotations:      ToolAnnotations{DestructiveHint: true, IdempotentHint: true},
		AuthorityRole:    durableSession.RolePlanner,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2AbandonInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			projectID, err := s.boundTrainProject(ctx)
			if err != nil {
				return nil, err
			}
			in.ProjectID = projectID
			return s.Service.TrainV2AbandonAsync(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:             "train/abandon_status",
		Description:      "Read a bounded Train abandonment receipt.",
		InputSchema:      obj(map[string]any{"operation_id": str("Durable Train abandonment operation identifier.")}, "operation_id"),
		OutputSchema:     trainV2OutputSchema(),
		Annotations:      ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
		AuthorityRole:    durableSession.RolePlanner,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				OperationID string `json:"operation_id"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TrainV2RetirementOperationStatus(ctx, in.OperationID)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/retire",
		Description:  "Retire one proven stale Train using the bound session project.",
		InputSchema:  trainV2RetireSchema(),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2RetireInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			projectID, err := s.boundTrainProject(ctx)
			if err != nil {
				return nil, err
			}
			in.ProjectID = projectID
			return s.Service.TrainV2RetireAsync(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/retire_status",
		Description:  "Read a bounded Train retirement receipt.",
		InputSchema:  obj(map[string]any{"operation_id": str("Durable Train retirement operation identifier.")}, "operation_id"),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				OperationID string `json:"operation_id"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TrainV2RetirementOperationStatus(ctx, in.OperationID)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/reconcile",
		Description:  "Dry-run or apply bounded stale Train reconciliation for the bound session project.",
		InputSchema:  trainV2ReconcileSchema(),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2ReconcileInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			projectID, err := s.boundTrainProject(ctx)
			if err != nil {
				return nil, err
			}
			in.ProjectID = projectID
			return s.Service.TrainV2ReconcileAsync(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/reconcile_status",
		Description:  "Read a bounded Train reconciliation receipt.",
		InputSchema:  obj(map[string]any{"operation_id": str("Durable Train reconciliation operation identifier.")}, "operation_id"),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				OperationID string `json:"operation_id"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TrainV2RetirementOperationStatus(ctx, in.OperationID)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/create",
		Description:  "Create a non-running ordered train_v2 admission record.",
		InputSchema:  trainV2CreateSchema(),
		OutputSchema: trainV2AdmissionReceiptOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  false,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2CreateInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			receipt, err := s.Service.TrainV2CreateAsync(ctx, in)
			if err != nil {
				return nil, err
			}
			return receipt, nil
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/attempt-proof-recover",
		Description:  "Recover missing immutable proof for one succeeded TrainItem Attempt while preserving an accepted review.",
		InputSchema:  trainV2AttemptProofRecoverySchema(),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2AttemptProofRecoveryInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TrainV2AttemptProofRecoveryAsync(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/attempt-proof-recover_status",
		Description:  "Read the durable receipt for Train Attempt proof recovery.",
		InputSchema:  obj(map[string]any{"operation_id": str("Durable Train Attempt proof recovery operation identifier.")}, "operation_id"),
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
			return s.Service.TrainV2AttemptOperationStatus(ctx, input.OperationID)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/add",
		Description:  "Append ready Tasks to the unstarted tail of a train_v2.",
		InputSchema:  trainV2AddSchema(),
		OutputSchema: trainV2AdmissionReceiptOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  false,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2AddInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			receipt, err := s.Service.TrainV2AddAsync(ctx, in)
			if err != nil {
				return nil, err
			}
			return receipt, nil
		},
	}); err != nil {
		return err
	}
	return nil
}
