package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/session"
)

const taskLegacyRevisionPageSize = 20

func (s *Server) registerTaskLegacyRevisionActions() error {
	register := func(action GenericAction) error {
		action.AuthorityRole = session.RolePlanner
		action.SessionBound = true
		action.LocalReadOnly = true
		action.LocalReceiptOnly = true
		return s.RegisterGenericAction(action)
	}
	if err := register(GenericAction{
		Path:                 "debug/task_legacy_revision_list",
		Description:          "Read bounded legacy TaskRevision evidence without entering SharedLifecycle history.",
		InputSchema:          taskLegacyRevisionListSchema(),
		ExecutionInputSchema: taskLegacyRevisionExecutionSchema(taskLegacyRevisionListSchema()),
		OutputSchema:         taskLegacyRevisionListOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Task      string `json:"task"`
				Cursor    string `json:"cursor,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			page, err := s.Service.TaskRevisionListPage(ctx, in.Task, service.CollectionPageInput{Limit: taskLegacyRevisionPageSize, Cursor: in.Cursor})
			if err != nil {
				return nil, err
			}
			items := make([]any, 0, len(page.Revisions))
			for _, revision := range page.Revisions {
				if revision.ProjectID != in.ProjectID || revision.TaskID != in.Task {
					return nil, fmt.Errorf("legacy TaskRevision project or task ownership mismatch")
				}
				items = append(items, map[string]any{
					"revision_id":     revision.ID,
					"revision":        revision.TaskRevision,
					"revision_sha256": revision.RevisionSHA256,
					"title":           revision.Title,
					"status":          revision.Status,
					"created_at":      revision.CreatedAt,
				})
			}
			result := map[string]any{"task": in.Task, "revisions": items}
			if page.HasMore && page.NextCursor != "" {
				result["next_cursor"] = page.NextCursor
			}
			return result, nil
		},
	}); err != nil {
		return err
	}
	return register(GenericAction{
		Path:                 "debug/task_legacy_revision_read",
		Description:          "Read one validated legacy TaskRevision without entering SharedLifecycle history.",
		InputSchema:          taskLegacyRevisionReadSchema(),
		ExecutionInputSchema: taskLegacyRevisionExecutionSchema(taskLegacyRevisionReadSchema()),
		OutputSchema:         taskLegacyRevisionReadOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID  string `json:"project_id"`
				RevisionID string `json:"revision_id"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			revision, err := s.Service.TaskRevisionRead(ctx, in.RevisionID)
			if err != nil {
				return nil, err
			}
			if revision.ProjectID != in.ProjectID {
				return nil, fmt.Errorf("legacy TaskRevision project ownership mismatch")
			}
			return map[string]any{"revision": revision}, nil
		},
	})
}

func taskLegacyRevisionTaskSchema() map[string]any {
	task := str("Canonical legacy Task identifier.")
	task["pattern"] = `^[A-Z]{3}-TSK[0-9]+$`
	return task
}

func taskLegacyRevisionIDSchema() map[string]any {
	revisionID := str("Canonical legacy TaskRevision identifier.")
	revisionID["pattern"] = `^[A-Z]{3}-TSK[0-9]+\.REV[1-9][0-9]*$`
	return revisionID
}

func taskLegacyRevisionListSchema() map[string]any {
	return obj(map[string]any{"task": taskLegacyRevisionTaskSchema(), "cursor": str("Opaque legacy TaskRevision continuation token.")}, "task")
}

func taskLegacyRevisionReadSchema() map[string]any {
	return obj(map[string]any{"revision_id": taskLegacyRevisionIDSchema()}, "revision_id")
}

func taskLegacyRevisionExecutionSchema(public map[string]any) map[string]any {
	properties := map[string]any{"project_id": str("Session-derived project identity.")}
	if source, ok := public["properties"].(map[string]any); ok {
		for key, value := range source {
			properties[key] = value
		}
	}
	required := []string{"project_id"}
	if source, ok := public["required"].([]string); ok {
		required = append(required, source...)
	}
	return closedOutput(properties, required...)
}

func taskLegacyRevisionListOutputSchema() map[string]any {
	item := closedOutput(map[string]any{
		"revision_id": outputString(), "revision": outputInteger(), "revision_sha256": outputString(),
		"title": outputString(), "status": outputString(), "created_at": outputDateTime(),
	}, "revision_id", "revision", "revision_sha256", "title", "status", "created_at")
	return closedOutput(map[string]any{
		"task": outputString(), "revisions": outputArray(item), "next_cursor": outputString(),
	}, "task", "revisions")
}

func taskLegacyRevisionReadOutputSchema() map[string]any {
	legacy := closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "task_id": outputString(), "task_revision": outputInteger(),
		"revision_sha256": outputString(), "parent_task_revision": outputInteger(), "parent_task_sha256": outputString(),
		"project_id": outputString(), "title": outputString(), "type": outputString(), "objective": outputString(),
		"branch": outputString(), "base_revision": outputString(), "acceptance_criteria": outputArray(outputString()),
		"constraints": outputArray(outputString()), "required_gates": outputArray(outputString()),
		"workflow_policy_revision": outputInteger(), "operation_class": outputString(), "effective_ci_field": outputString(),
		"effective_ci_mode": outputString(), "wait_for_ci": outputBoolean(), "ci_blocking": outputBoolean(),
		"agent_may_wait": outputBoolean(), "status": outputString(), "source_train_id": outputString(),
		"source_item_position": outputInteger(), "source_attempt_number": outputInteger(), "created_by": outputString(),
		"created_at": outputDateTime(),
	}, "schema_version", "id", "task_id", "task_revision", "revision_sha256", "project_id", "title", "objective", "branch", "acceptance_criteria", "constraints", "workflow_policy_revision", "operation_class", "effective_ci_field", "effective_ci_mode", "wait_for_ci", "ci_blocking", "agent_may_wait", "status", "created_by", "created_at")
	return closedOutput(map[string]any{"revision": legacy}, "revision")
}
