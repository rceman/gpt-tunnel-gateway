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

func trainV2OutputSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}

func trainV2CreateSchema() map[string]any {
	return obj(map[string]any{
		"project_id":            str("Registered project identifier."),
		"task_ids":              array(str("Ready Task identifier.")),
		"created_by":            str("Author identity."),
		"expected_hub_revision": str("Optimistic Hub revision."),
	}, "project_id", "task_ids", "created_by")
}

func trainV2AddSchema() map[string]any {
	return obj(map[string]any{
		"project_id":            str("Registered project identifier."),
		"train_id":              str("Server-allocated Train identifier."),
		"task_ids":              array(str("Ready Task identifier.")),
		"expected_revision":     integer("Exact Train revision.", 1, 1000000),
		"added_by":              str("Author identity."),
		"expected_hub_revision": str("Optimistic Hub revision."),
	}, "project_id", "train_id", "task_ids", "expected_revision", "added_by")
}

func trainV2ReadSchema() map[string]any {
	return obj(map[string]any{"project_id": str("Registered project identifier."), "train_id": str("Server-allocated Train identifier.")}, "project_id", "train_id")
}

func trainV2ListSchema() map[string]any {
	return obj(map[string]any{"project_id": str("Registered project identifier."), "limit": integer("Maximum Trains.", 1, 32)}, "project_id")
}

func trainV2StartSchema() map[string]any {
	return obj(map[string]any{
		"project_id":            str("Registered project identifier."),
		"train_id":              str("Server-allocated Train identifier."),
		"started_by":            str("Author identity."),
		"agent_id":              str("Optional coding Agent identity."),
		"recommended_reasoning": str("Optional reasoning preference."),
		"expected_hub_revision": str("Optimistic Hub revision."),
	}, "project_id", "train_id", "started_by")
}

func trainV2IntegrateSchema() map[string]any {
	return obj(map[string]any{
		"project_id":            str("Registered project identifier."),
		"train_id":              str("Server-allocated Train identifier."),
		"expected_hub_revision": str("Optimistic Hub revision."),
	}, "project_id", "train_id")
}

func trainV2CutoverSchema() map[string]any {
	return obj(map[string]any{
		"project_id":                   str("Registered project identifier."),
		"materialization_acknowledged": map[string]any{"type": "boolean", "description": "Explicit acknowledgement that relevant roadmap work was materialized or archived."},
		"plan_retirement_acknowledged": map[string]any{"type": "boolean", "description": "Explicit acknowledgement that Plan becomes historical/read-only."},
		"updated_by":                   str("Authority identity."),
		"expected_hub_revision":        str("Optimistic Hub revision."),
	}, "project_id", "materialization_acknowledged", "plan_retirement_acknowledged", "updated_by")
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
		Path: "train/create", Description: "Create a non-running ordered train_v2 admission record.", InputSchema: trainV2CreateSchema(), OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{DestructiveHint: true, IdempotentHint: false}, AuthorityRole: actionRolePlannerOrDelivery,
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
		Path: "train/add", Description: "Append ready Tasks to the unstarted tail of a train_v2.", InputSchema: trainV2AddSchema(), OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{DestructiveHint: true, IdempotentHint: false}, AuthorityRole: actionRolePlannerOrDelivery,
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
		Path: "train/read", Description: "Read one train_v2 admission record.", InputSchema: trainV2ReadSchema(), OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}, AuthorityRole: actionRolePlannerOrDelivery,
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
		Path: "train/list", Description: "List bounded train_v2 admission records.", InputSchema: trainV2ListSchema(), OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}, AuthorityRole: actionRolePlannerOrDelivery,
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
		Path: "train/start", Description: "Start one server-owned Train v2 execution lane.", InputSchema: trainV2StartSchema(), OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{DestructiveHint: true, IdempotentHint: true}, AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2StartInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.TrainV2Start(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path: "train/integrate", Description: "Integrate one fully proved Train v2 lane through strict fast-forward and activation receipts.", InputSchema: trainV2IntegrateSchema(), OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{DestructiveHint: true, IdempotentHint: true}, AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TrainV2IntegrateInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			receipt, operation, err := s.Service.TrainV2Integrate(ctx, in)
			if err != nil {
				return map[string]any{"receipt": receipt, "operation": operation}, err
			}
			return map[string]any{"receipt": receipt, "operation": operation}, nil
		},
	}); err != nil {
		return err
	}
	return s.RegisterGenericAction(GenericAction{
		Path: "train/cutover", Description: "Atomically activate train_v2 authority after bounded migration, runtime and Action Registry proofs.", InputSchema: trainV2CutoverSchema(), OutputSchema: trainV2OutputSchema(),
		Annotations: ToolAnnotations{DestructiveHint: true, IdempotentHint: true}, AuthorityRole: actionRolePlannerOrDelivery,
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
