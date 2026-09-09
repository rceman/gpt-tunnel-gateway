package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func taskLifecycleValue(task model.TaskAuthoring) map[string]any {
	value := map[string]any{"key": task.ID, "revision": task.Revision, "title": task.Title, "status": task.Status, "type": task.Type, "objective": task.Objective, "acceptance_criteria": task.AcceptanceCriteria, "constraints": task.Constraints, "dependencies": task.Dependencies, "preparation_references": task.PreparationReferences, "adr_relation": task.ADRRelation, "adr_references": task.ADRReferences, "created_at": task.CreatedAt}
	if task.Summary != "" {
		value["summary"] = task.Summary
	}
	if task.Scope != nil {
		value["scope"] = task.Scope
	}
	if task.Priority != "" {
		value["priority"] = task.Priority
	}
	if task.Metadata != nil {
		value["metadata"] = task.Metadata
	}
	if task.Revision >= 2 {
		value["updated_at"] = task.UpdatedAt
	}
	return value
}

func (s *Server) registerTaskAuthoringActions() error {
	register := func(action GenericAction) error {
		action.AuthorityRole = durableSession.RolePlanner
		action.SessionBound = true
		action.LocalReceiptOnly = true
		return s.RegisterGenericAction(action)
	}
	if err := register(GenericAction{
		Path:                 "task/create",
		Description:          "Create one revisioned Task.",
		InputSchema:          taskLifecycleCreateSchema(),
		ExecutionInputSchema: adrExecutionSchema(taskLifecycleCreateSchema()),
		OutputSchema:         taskLifecycleOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID             string            `json:"project_id"`
				Type                  model.TaskType    `json:"type,omitempty"`
				Scope                 *model.TaskScope  `json:"scope,omitempty"`
				Title                 string            `json:"title"`
				Summary               string            `json:"summary"`
				Objective             string            `json:"objective"`
				AcceptanceCriteria    []string          `json:"acceptance_criteria"`
				Constraints           []string          `json:"constraints"`
				Priority              string            `json:"priority,omitempty"`
				Dependencies          []string          `json:"dependencies,omitempty"`
				PreparationReferences []string          `json:"preparation_references,omitempty"`
				Metadata              map[string]string `json:"metadata,omitempty"`
				ADRRelation           string            `json:"adr_relation"`
				ADRReferences         []string          `json:"adr_references,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor := service.AgentSessionID(ctx)
			if actor == "" {
				return nil, fmt.Errorf("authorized session actor is unavailable")
			}
			task, _, err := s.Service.TaskLifecycleCreate(ctx, service.TaskAuthoringCreateInput{ProjectID: in.ProjectID, Type: in.Type, Scope: in.Scope, Title: in.Title, Summary: in.Summary, Objective: in.Objective, AcceptanceCriteria: in.AcceptanceCriteria, Constraints: in.Constraints, Priority: in.Priority, Dependencies: in.Dependencies, PreparationReferences: in.PreparationReferences, Metadata: in.Metadata, ADRRelation: in.ADRRelation, ADRReferences: in.ADRReferences, CreatedBy: actor}, "")
			if err != nil {
				return nil, err
			}
			return map[string]any{"key": task.ID, "revision": task.Revision}, nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "task/read",
		Description:          "Read one Task revision.",
		InputSchema:          taskLifecycleReadSchema(),
		ExecutionInputSchema: adrExecutionSchema(taskLifecycleReadSchema()),
		OutputSchema:         taskLifecycleReadOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Key       string `json:"key"`
				Revision  int    `json:"revision,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			task, err := s.Service.TaskLifecycleRead(ctx, in.ProjectID, in.Key, in.Revision)
			if err != nil {
				return nil, err
			}
			return taskLifecycleValue(task), nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "task/update",
		Description:          "Update Task content with server-owned CAS.",
		InputSchema:          taskLifecycleUpdateSchema(),
		ExecutionInputSchema: adrExecutionSchema(taskLifecycleUpdateSchema()),
		OutputSchema:         taskLifecycleOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TaskAuthoringUpdateInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			in.ProjectID = projectIDFromTaskRaw(raw)
			in.TaskID = taskIDFromTaskRaw(raw)
			in.Reason = reasonFromTaskRaw(raw)
			in.UpdatedBy = service.AgentSessionID(ctx)
			if in.UpdatedBy == "" {
				return nil, fmt.Errorf("authorized session actor is unavailable")
			}
			current, err := s.Service.TaskAuthoringRead(ctx, in.ProjectID, in.TaskID)
			if err != nil {
				return nil, err
			}
			in.ExpectedRevision = current.Revision
			task, _, err := s.Service.TaskLifecycleUpdate(ctx, in)
			if err != nil {
				return nil, err
			}
			return map[string]any{"key": task.ID, "revision": task.Revision}, nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "task/list",
		Description:          "List bounded Task summaries.",
		InputSchema:          taskLifecycleListSchema(),
		ExecutionInputSchema: adrExecutionSchema(taskLifecycleListSchema()),
		OutputSchema:         taskLifecycleListOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID       string `json:"project_id"`
				Cursor          string `json:"cursor,omitempty"`
				IncludeArchived bool   `json:"include_archived,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			page, err := s.Service.TaskLifecycleListQuery(ctx, in.ProjectID, "", "", "", in.Cursor, in.IncludeArchived)
			if err != nil {
				return nil, err
			}
			return taskPageValue(page), nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "task/query",
		Description:          "Query bounded Task summaries.",
		InputSchema:          taskLifecycleQuerySchema(),
		ExecutionInputSchema: adrExecutionSchema(taskLifecycleQuerySchema()),
		OutputSchema:         taskLifecycleListOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string         `json:"project_id"`
				Cursor    string         `json:"cursor,omitempty"`
				Text      string         `json:"text,omitempty"`
				Status    string         `json:"status,omitempty"`
				Type      model.TaskType `json:"type,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			page, err := s.Service.TaskLifecycleListQuery(ctx, in.ProjectID, in.Text, in.Status, in.Type, in.Cursor, in.Status == model.TaskAuthoringArchived)
			if err != nil {
				return nil, err
			}
			return taskPageValue(page), nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "task/archive",
		Description:          "Archive one Task.",
		InputSchema:          taskLifecycleArchiveSchema(),
		ExecutionInputSchema: adrExecutionSchema(taskLifecycleArchiveSchema()),
		OutputSchema:         taskLifecycleOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Key       string `json:"key"`
				Reason    string `json:"reason"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor := service.AgentSessionID(ctx)
			if actor == "" {
				return nil, fmt.Errorf("authorized session actor is unavailable")
			}
			task, err := s.Service.TaskLifecycleArchive(ctx, in.ProjectID, in.Key, actor, in.Reason)
			if err != nil {
				return nil, err
			}
			return map[string]any{"key": task.ID, "revision": task.Revision}, nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "task/history",
		Description:          "Read bounded Task revision history.",
		InputSchema:          taskLifecycleHistorySchema(),
		ExecutionInputSchema: adrExecutionSchema(taskLifecycleHistorySchema()),
		OutputSchema:         taskLifecycleHistoryOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Key       string `json:"key"`
				Cursor    string `json:"cursor,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			page, err := s.Service.TaskLifecycleHistory(ctx, in.ProjectID, in.Key, in.Cursor)
			if err != nil {
				return nil, err
			}
			rows := make([]any, 0, len(page.Records))
			for _, record := range page.Records {
				rows = append(rows, map[string]any{"revision": record.Revision, "mutation_kind": record.MutationKind, "actor": record.Actor, "reason": record.Reason, "changed_fields": record.ChangedFields, "recorded_at": record.RecordedAt})
			}
			result := map[string]any{"key": in.Key, "revisions": rows}
			if page.HasMore {
				result["next_cursor"] = pagination.EncodeOpaqueKeyset("task-history:"+in.ProjectID+":"+in.Key, fmt.Sprintf("%d", page.NextRevision))
			}
			return result, nil
		},
	}); err != nil {
		return err
	}
	return s.registerTaskExecutionActions()
}

func projectIDFromTaskRaw(raw json.RawMessage) string {
	var value struct {
		ProjectID string `json:"project_id"`
	}
	_ = json.Unmarshal(raw, &value)
	return value.ProjectID
}

func taskIDFromTaskRaw(raw json.RawMessage) string {
	var value struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(raw, &value)
	return value.Key
}

func reasonFromTaskRaw(raw json.RawMessage) string {
	var value struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(raw, &value)
	return value.Reason
}
func taskPageValue(page service.TaskLifecyclePage) map[string]any {
	tasks := make([]any, 0, len(page.Tasks))
	for _, task := range page.Tasks {
		item := map[string]any{"key": task.ID, "title": task.Title, "summary": task.Summary, "status": task.Status, "revision": task.Revision}
		if task.Revision >= 2 {
			item["updated_at"] = task.UpdatedAt
		}
		tasks = append(tasks, item)
	}
	result := map[string]any{"items": tasks}
	if page.HasMore && page.NextCursor != "" {
		result["next_cursor"] = page.NextCursor
	}
	return result
}
