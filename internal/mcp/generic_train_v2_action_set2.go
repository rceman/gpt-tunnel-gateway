package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) registerTrainV2ActionSet2() error {
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "train/read",
		Description:  "Read one train_v2 admission record.",
		InputSchema:  trainV2ReadSchema(),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		AuthorityRole: "planner",
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
		AuthorityRole: "planner",
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
		AuthorityRole:    "planner",
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
		Path:         "train/advance",
		Description:  "Start the next queued TrainItem Attempt without creating a global Run.",
		InputSchema:  trainV2AdvanceSchema(),
		OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		AuthorityRole:    "planner",
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
	return nil
}
