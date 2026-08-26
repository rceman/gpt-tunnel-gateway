package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) registerTrainV2ActionSet1() error {
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
