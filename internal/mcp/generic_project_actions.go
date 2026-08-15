package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) ensureProjectActions() {
	if s.Service == nil {
		return
	}
	s.projectActions.Do(func() {
		s.projectActionErr = s.registerProjectActions()
	})
	if s.projectActionErr != nil {
		panic(s.projectActionErr)
	}
}

func projectConfigurationObjectSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}

func projectConfigurationUpdateSchema() map[string]any {
	gateCommand := obj(map[string]any{
		"command": array(str("Repository-owned argv; no shell expansion.")),
	}, "command")
	gateCommands := obj(map[string]any{
		"format": gateCommand,
		"check":  gateCommand,
		"test": obj(map[string]any{
			"task":  gateCommand,
			"train": gateCommand,
		}, "task", "train"),
	}, "format", "check", "test")
	return obj(map[string]any{
		"project_id":        str("Registered project identifier."),
		"expected_revision": integer("Exact current project configuration revision.", 1, 1000000),
		"patch": obj(map[string]any{
			"agent_routing": obj(map[string]any{
				"singleton_recommended_reasoning": str("Default reasoning tier for singleton tasks."),
				"group_recommended_reasoning":     str("Default reasoning tier for task groups."),
				"fallback":                        str("Agent resolution fallback; must be best_available."),
			}, "singleton_recommended_reasoning", "group_recommended_reasoning", "fallback"),
			"watcher": obj(map[string]any{
				"agent_id":        str("Project-scoped watcher Agent identity."),
				"mode":            str("Watcher mode: disabled, observe, or require."),
				"cadence_seconds": integer("Watcher cadence in seconds.", 1, 3600),
				"tail_lines":      integer("Watcher tail bound.", 1, model.WatcherMaxTailLines),
				"seen_retention":  integer("Watcher digest retention.", 1, model.WatcherMaxSeenDigests),
				"nudge_enabled":   map[string]any{"type": "boolean"},
				"restart_enabled": map[string]any{"type": "boolean"},
			}, "mode", "cadence_seconds", "tail_lines", "seen_retention", "nudge_enabled", "restart_enabled"),
			"workflow": obj(map[string]any{
				"workflow_stage":     str("Project workflow stage."),
				"integration_branch": str("Canonical integration branch."),
				"ci":                 map[string]any{"type": "object", "additionalProperties": true},
				"gates":              array(str("Declared workflow gate.")),
				"gate_commands":      gateCommands,
				"wait_for_ci":        map[string]any{"type": "boolean"},
			}, "workflow_stage", "integration_branch", "ci", "gates", "gate_commands", "wait_for_ci"),
			"activation_profile_ref": str("Portable activation profile reference."),
			"integration": obj(map[string]any{
				"target_branch": str("Project-owned integration target branch."),
				"pre":           gateCommand,
				"post":          gateCommand,
			}, "target_branch"),
			"gate_commands": gateCommands,
		}),
		"updated_by":            str("Trusted mutation author."),
		"expected_hub_revision": str("Optional exact Hub revision guard."),
	}, "project_id", "expected_revision", "patch", "updated_by")
}

func (s *Server) registerProjectActions() error {
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "project/update",
		Description:  "Update the durable revisioned portable project configuration.",
		InputSchema:  projectConfigurationUpdateSchema(),
		OutputSchema: projectConfigurationMutationReceiptOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  false,
		},
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.ProjectConfigurationUpdateInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			receipt, err := s.Service.ProjectConfigurationUpdateAsync(ctx, in)
			if err != nil {
				return nil, err
			}
			return receipt, nil
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:         "project/update_status",
		Description:  "Read the durable receipt for an asynchronous project/update operation.",
		InputSchema:  obj(map[string]any{"operation_id": str("Durable project configuration operation identifier.")}, "operation_id"),
		OutputSchema: projectConfigurationMutationReceiptOutputSchema(),
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
			return s.Service.ProjectConfigurationUpdateOperationStatus(ctx, in.OperationID)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:             "project/remove_status",
		Description:      "Read the durable receipt for an asynchronous project/remove operation.",
		InputSchema:      obj(map[string]any{"operation_id": str("Durable project removal operation identifier.")}, "operation_id"),
		OutputSchema:     projectRemoveReceiptOutputSchema(),
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
			return s.Service.ProjectRemoveOperationStatus(ctx, input.OperationID)
		},
	}); err != nil {
		return err
	}
	return s.RegisterGenericAction(GenericAction{
		Path:        "project/remove",
		Description: "Remove one managed project from the active Gateway registry after fail-closed authority checks; the external repository is never touched.",
		InputSchema: obj(map[string]any{
			"project_id":            str("Managed project identifier."),
			"expected_hub_revision": str("Optional exact Hub revision guard."),
		}, "project_id"),
		OutputSchema: projectRemoveReceiptOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Authority:        service.RequireWorkflowPolicyAuthority,
		AuthorityRole:    actionRolePlannerOrDelivery,
		LocalReceiptOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.ProjectRemoveInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.ProjectRemoveAsync(ctx, in)
		},
	})
}
