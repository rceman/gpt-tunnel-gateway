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
				"wait_for_ci":        map[string]any{"type": "boolean"},
			}, "workflow_stage", "integration_branch", "ci", "gates", "wait_for_ci"),
			"activation_profile_ref": str("Portable activation profile reference."),
		}),
		"updated_by":            str("Trusted mutation author."),
		"expected_hub_revision": str("Optional exact Hub revision guard."),
	}, "project_id", "expected_revision", "patch", "updated_by")
}

func (s *Server) registerProjectActions() error {
	return s.RegisterGenericAction(GenericAction{
		Path:          "project/update",
		Description:   "Update the durable revisioned portable project configuration.",
		InputSchema:   projectConfigurationUpdateSchema(),
		OutputSchema:  map[string]any{"type": "object", "additionalProperties": true},
		Annotations:   ToolAnnotations{DestructiveHint: true, IdempotentHint: false},
		AuthorityRole: actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.ProjectConfigurationUpdateInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			configuration, operation, err := s.Service.ProjectConfigurationUpdate(ctx, in)
			if err != nil {
				return nil, err
			}
			return map[string]any{"configuration": configuration, "operation": operation}, nil
		},
	})
}
