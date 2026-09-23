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

func milestoneStatusesSchema() map[string]any {
	return outputEnum(model.MilestoneStatuses()...)
}

func milestoneTaskInputSchema() map[string]any {
	return boundedADRString("Canonical Task identifier.", 8, 128)
}

func milestoneTaskOutputSchema() map[string]any {
	return closedOutput(map[string]any{"key": outputString(), "title": outputString(), "status": outputString(), "priority": outputString()}, "key", "title", "status")
}

func milestoneProperties() map[string]any {
	return map[string]any{
		"key":              outputString(),
		"revision":         outputInteger(),
		"title":            boundedADRString("Milestone title.", 3, 128),
		"summary":          boundedADRString("Bounded Milestone summary.", 0, 256),
		"status":           milestoneStatusesSchema(),
		"tasks":            outputArray(milestoneTaskInputSchema()),
		"reason":           boundedADRString("Bounded mutation reason.", 1, 1024),
		"evidence":         boundedADRString("Bounded completion evidence.", 1, model.MilestoneMaxEvidenceBytes),
		"evidence_refs":    outputArray(boundedADRString("Canonical evidence entity reference.", 8, 128)),
		"include_archived": outputBoolean(),
		"text":             outputString(),
		"cursor":           publicServerCursorSchema(),
	}
}

func milestoneCreateSchema() map[string]any {
	p := milestoneProperties()
	return obj(map[string]any{"title": p["title"], "summary": p["summary"], "tasks": p["tasks"]}, "title")
}

func milestoneReadSchema() map[string]any {
	p := milestoneProperties()
	return obj(map[string]any{"key": p["key"], "revision": p["revision"]}, "key")
}

func milestoneUpdateSchema() map[string]any {
	p := milestoneProperties()
	return obj(map[string]any{"key": p["key"], "title": p["title"], "summary": p["summary"], "reason": p["reason"]}, "key", "reason")
}

func milestoneMembershipSchema() map[string]any {
	p := milestoneProperties()
	return obj(map[string]any{"key": p["key"], "tasks": p["tasks"], "reason": p["reason"]}, "key", "tasks", "reason")
}

func milestoneListSchema() map[string]any {
	p := milestoneProperties()
	return obj(map[string]any{"cursor": p["cursor"], "include_archived": p["include_archived"]})
}

func milestoneQuerySchema() map[string]any {
	p := milestoneProperties()
	return obj(map[string]any{"cursor": p["cursor"], "text": p["text"], "status": p["status"]})
}

func milestoneArchiveSchema() map[string]any {
	p := milestoneProperties()
	return obj(map[string]any{"key": p["key"], "reason": p["reason"]}, "key", "reason")
}

func milestoneHistorySchema() map[string]any {
	p := milestoneProperties()
	return obj(map[string]any{"key": p["key"], "cursor": p["cursor"]}, "key")
}

func milestoneActivateSchema() map[string]any {
	p := milestoneProperties()
	return obj(map[string]any{"key": p["key"]}, "key")
}

func milestoneCompleteSchema() map[string]any {
	p := milestoneProperties()
	return obj(map[string]any{"key": p["key"], "evidence": p["evidence"], "evidence_refs": p["evidence_refs"]}, "key", "evidence")
}

func milestoneMutationOutputSchema() map[string]any {
	return closedOutput(map[string]any{"key": outputString(), "revision": outputInteger()}, "key", "revision")
}

func milestoneReadOutputSchema() map[string]any {
	p := milestoneProperties()
	properties := map[string]any{"key": p["key"], "revision": p["revision"], "title": p["title"], "summary": p["summary"], "status": p["status"], "tasks": outputArray(milestoneTaskOutputSchema()), "report": outputString(), "created_at": outputDateTime(), "updated_at": outputDateTime(), "completion_evidence": p["evidence"], "completion_evidence_refs": p["evidence_refs"]}
	return closedOutput(properties, "key", "revision", "title", "status", "tasks", "report", "created_at")
}

func milestoneSummaryOutputSchema() map[string]any {
	p := milestoneProperties()
	return closedOutput(map[string]any{"key": p["key"], "revision": p["revision"], "title": p["title"], "summary": p["summary"], "status": p["status"], "updated_at": outputDateTime()}, "key", "revision", "title", "status")
}

func milestoneListOutputSchema() map[string]any {
	return closedOutput(map[string]any{"items": outputArray(milestoneSummaryOutputSchema())}, "items")
}

func milestoneHistoryOutputSchema() map[string]any {
	row := closedOutput(map[string]any{"revision": outputInteger(), "mutation_kind": outputString(), "actor": outputString(), "reason": outputString(), "changed_fields": outputArray(outputString()), "recorded_at": outputDateTime()}, "revision", "mutation_kind", "actor", "reason", "recorded_at")
	return closedOutput(map[string]any{"key": outputString(), "items": outputArray(row)}, "key", "items")
}

func milestoneViewValue(view service.MilestoneView) map[string]any {
	m := view.Milestone
	tasks := make([]any, 0, len(view.Tasks))
	for _, task := range view.Tasks {
		value := map[string]any{"key": task.Key, "title": task.Title, "status": task.Status}
		if task.Priority != "" {
			value["priority"] = task.Priority
		}
		tasks = append(tasks, value)
	}
	value := map[string]any{"key": m.ID, "revision": m.Revision, "title": m.Title, "status": m.Status, "tasks": tasks, "report": view.Report, "created_at": m.CreatedAt}
	if m.Summary != "" {
		value["summary"] = m.Summary
	}
	if m.Revision > 1 || !m.UpdatedAt.Equal(m.CreatedAt) {
		value["updated_at"] = m.UpdatedAt
	}
	if m.CompletionEvidence != "" {
		value["completion_evidence"] = m.CompletionEvidence
	}
	if len(m.CompletionEvidenceRefs) > 0 {
		value["completion_evidence_refs"] = m.CompletionEvidenceRefs
	}
	return value
}

func milestoneSummaryValue(view service.MilestoneView) map[string]any {
	milestone := view.Milestone
	value := map[string]any{"key": milestone.ID, "revision": milestone.Revision, "title": milestone.Title, "status": milestone.Status}
	if milestone.Summary != "" {
		value["summary"] = milestone.Summary
	}
	if milestone.Revision > 1 || !milestone.UpdatedAt.Equal(milestone.CreatedAt) {
		value["updated_at"] = milestone.UpdatedAt
	}
	return value
}

func (s *Server) ensureMilestoneActions() {
	s.milestoneActions.Do(func() { s.milestoneActionErr = s.registerMilestoneActions() })
	if s.milestoneActionErr != nil {
		panic(s.milestoneActionErr)
	}
}

func (s *Server) registerMilestoneActions() error {
	registerPlanner := func(action GenericAction) error {
		action.AuthorityRole = durableSession.RolePlanner
		action.SessionBound = true
		action.LocalReceiptOnly = true
		return s.RegisterGenericAction(action)
	}
	registerRead := func(action GenericAction) error {
		action.AuthorityRole = actionRoleWorkflow
		action.SessionBound = true
		action.LocalReceiptOnly = true
		return s.RegisterGenericAction(action)
	}
	if err := registerPlanner(GenericAction{
		Path:                 "milestone/create",
		Description:          "Create one planned Milestone with an unordered Task membership set.",
		InputSchema:          milestoneCreateSchema(),
		ExecutionInputSchema: adrExecutionSchema(milestoneCreateSchema()),
		OutputSchema:         milestoneMutationOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string   `json:"project_id"`
				Title     string   `json:"title"`
				Summary   string   `json:"summary"`
				Tasks     []string `json:"tasks"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor := service.AgentSessionID(ctx)
			if actor == "" {
				return nil, fmt.Errorf("authorized session actor is unavailable")
			}
			m, result, err := s.Service.MilestoneLifecycleCreate(ctx, service.MilestoneCreateInput{ProjectID: in.ProjectID, Title: in.Title, Summary: in.Summary, Tasks: in.Tasks, CreatedBy: actor}, "")
			if err != nil {
				return nil, err
			}
			return map[string]any{"key": m.ID, "revision": result.Revision}, nil
		},
	}); err != nil {
		return err
	}
	if err := registerRead(GenericAction{
		Path:                 "milestone/read",
		Description:          "Read one Milestone with live ordered Task projections.",
		InputSchema:          milestoneReadSchema(),
		ExecutionInputSchema: adrExecutionSchema(milestoneReadSchema()),
		OutputSchema:         milestoneReadOutputSchema(),
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
			view, err := s.Service.MilestoneLifecycleView(ctx, in.ProjectID, in.Key, in.Revision)
			if err != nil {
				return nil, err
			}
			return milestoneViewValue(view), nil
		},
	}); err != nil {
		return err
	}
	if err := registerRead(GenericAction{
		Path: "milestone/plan",
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Key       string `json:"key"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			markdown, err := s.Service.MilestoneLifecyclePlan(ctx, in.ProjectID, in.Key)
			if err != nil {
				return nil, err
			}
			return map[string]any{"markdown": markdown}, nil
		},
	}); err != nil {
		return err
	}
	if err := registerPlanner(GenericAction{
		Path:                 "milestone/update",
		Description:          "Update Milestone metadata without changing Task membership.",
		InputSchema:          milestoneUpdateSchema(),
		ExecutionInputSchema: adrExecutionSchema(milestoneUpdateSchema()),
		OutputSchema:         milestoneMutationOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string  `json:"project_id"`
				Key       string  `json:"key"`
				Reason    string  `json:"reason"`
				Title     *string `json:"title,omitempty"`
				Summary   *string `json:"summary,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor := service.AgentSessionID(ctx)
			if actor == "" {
				return nil, fmt.Errorf("authorized session actor is unavailable")
			}
			current, err := s.Service.MilestoneLifecycleRead(ctx, in.ProjectID, in.Key, 0)
			if err != nil {
				return nil, err
			}
			m, result, err := s.Service.MilestoneLifecycleUpdate(ctx, service.MilestoneUpdateInput{ProjectID: in.ProjectID, Key: in.Key, Title: in.Title, Summary: in.Summary, ExpectedRevision: current.Revision, UpdatedBy: actor, Reason: in.Reason})
			if err != nil {
				return nil, err
			}
			return map[string]any{"key": m.ID, "revision": result.Revision}, nil
		},
	}); err != nil {
		return err
	}
	for _, membership := range []struct {
		path        string
		description string
		appendTasks bool
	}{
		{path: "milestone/append_task", description: "Add a batch of canonical Tasks to an unordered Milestone set.", appendTasks: true},
		{path: "milestone/remove_task", description: "Remove a batch of canonical Tasks from an unordered Milestone set.", appendTasks: false},
	} {
		membership := membership
		if err := registerPlanner(GenericAction{
			Path:                 membership.path,
			Description:          membership.description,
			InputSchema:          milestoneMembershipSchema(),
			ExecutionInputSchema: adrExecutionSchema(milestoneMembershipSchema()),
			OutputSchema:         milestoneMutationOutputSchema(),
			Annotations: ToolAnnotations{
				DestructiveHint: true,
				IdempotentHint:  true,
			},
			Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var in struct {
					ProjectID string   `json:"project_id"`
					Key       string   `json:"key"`
					Tasks     []string `json:"tasks"`
					Reason    string   `json:"reason"`
				}
				if err := decode(raw, &in); err != nil {
					return nil, err
				}
				actor := service.AgentSessionID(ctx)
				if actor == "" {
					return nil, fmt.Errorf("authorized session actor is unavailable")
				}
				input := service.MilestoneMembershipInput{ProjectID: in.ProjectID, Key: in.Key, Tasks: in.Tasks, Actor: actor, Reason: in.Reason}
				var milestone model.Milestone
				var result service.OperationResult
				var err error
				if membership.appendTasks {
					milestone, result, err = s.Service.MilestoneLifecycleAppendTasks(ctx, input)
				} else {
					milestone, result, err = s.Service.MilestoneLifecycleRemoveTasks(ctx, input)
				}
				if err != nil {
					return nil, err
				}
				return map[string]any{"key": milestone.ID, "revision": result.Revision}, nil
			},
		}); err != nil {
			return err
		}
	}
	if err := registerRead(GenericAction{
		Path:                 "milestone/list",
		Description:          "List bounded Milestone reports with live ordered Task projections.",
		InputSchema:          milestoneListSchema(),
		ExecutionInputSchema: adrExecutionSchema(milestoneListSchema()),
		OutputSchema:         milestoneListOutputSchema(),
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
			page, err := s.Service.MilestoneLifecycleListQuery(ctx, in.ProjectID, "", "", in.Cursor, in.IncludeArchived)
			if err != nil {
				return nil, err
			}
			return milestonePageValue(page)
		},
	}); err != nil {
		return err
	}
	if err := registerRead(GenericAction{
		Path:                 "milestone/query",
		Description:          "Query bounded Milestone reports.",
		InputSchema:          milestoneQuerySchema(),
		ExecutionInputSchema: adrExecutionSchema(milestoneQuerySchema()),
		OutputSchema:         milestoneListOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Cursor    string `json:"cursor,omitempty"`
				Text      string `json:"text,omitempty"`
				Status    string `json:"status,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			page, err := s.Service.MilestoneLifecycleListQuery(ctx, in.ProjectID, in.Text, in.Status, in.Cursor, in.Status == model.MilestoneArchived)
			if err != nil {
				return nil, err
			}
			return milestonePageValue(page)
		},
	}); err != nil {
		return err
	}
	if err := registerPlanner(GenericAction{
		Path:                 "milestone/archive",
		Description:          "Archive one completed Milestone.",
		InputSchema:          milestoneArchiveSchema(),
		ExecutionInputSchema: adrExecutionSchema(milestoneArchiveSchema()),
		OutputSchema:         milestoneMutationOutputSchema(),
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
			m, err := s.Service.MilestoneLifecycleArchive(ctx, in.ProjectID, in.Key, actor, in.Reason)
			if err != nil {
				return nil, err
			}
			return map[string]any{"key": m.ID, "revision": m.Revision}, nil
		},
	}); err != nil {
		return err
	}
	if err := registerRead(GenericAction{
		Path:                 "milestone/history",
		Description:          "Read bounded Milestone revision and lifecycle history.",
		InputSchema:          milestoneHistorySchema(),
		ExecutionInputSchema: adrExecutionSchema(milestoneHistorySchema()),
		OutputSchema:         milestoneHistoryOutputSchema(),
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
			page, err := s.Service.MilestoneLifecycleHistory(ctx, in.ProjectID, in.Key, in.Cursor)
			if err != nil {
				return nil, err
			}
			result := map[string]any{"key": in.Key, "items": make([]any, 0, len(page.Records))}
			items := result["items"].([]any)
			for _, record := range page.Records {
				items = append(items, map[string]any{"revision": record.Revision, "mutation_kind": record.MutationKind, "actor": record.Actor, "reason": record.Reason, "changed_fields": record.ChangedFields, "recorded_at": record.RecordedAt})
			}
			result["items"] = items
			cursor := pagination.EncodeOpaqueKeyset("milestone-history:"+in.ProjectID+":"+in.Key, page.NextCursor)
			return genericActionPageResult(result, page.HasMore, cursor)
		},
	}); err != nil {
		return err
	}
	if err := registerPlanner(GenericAction{
		Path:                 "milestone/activate",
		Description:          "Explicitly activate one planned Milestone.",
		InputSchema:          milestoneActivateSchema(),
		ExecutionInputSchema: adrExecutionSchema(milestoneActivateSchema()),
		OutputSchema:         milestoneMutationOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
				Key       string `json:"key"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor := service.AgentSessionID(ctx)
			if actor == "" {
				return nil, fmt.Errorf("authorized session actor is unavailable")
			}
			m, err := s.Service.MilestoneLifecycleActivate(ctx, in.ProjectID, in.Key, actor)
			if err != nil {
				return nil, err
			}
			return map[string]any{"key": m.ID, "revision": m.Revision}, nil
		},
	}); err != nil {
		return err
	}
	if err := registerPlanner(GenericAction{
		Path:                 "milestone/complete",
		Description:          "Complete an active Milestone after every member Task is done.",
		InputSchema:          milestoneCompleteSchema(),
		ExecutionInputSchema: adrExecutionSchema(milestoneCompleteSchema()),
		OutputSchema:         milestoneMutationOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID    string   `json:"project_id"`
				Key          string   `json:"key"`
				Evidence     string   `json:"evidence"`
				EvidenceRefs []string `json:"evidence_refs,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor := service.AgentSessionID(ctx)
			if actor == "" {
				return nil, fmt.Errorf("authorized session actor is unavailable")
			}
			m, err := s.Service.MilestoneLifecycleComplete(ctx, service.MilestoneCompletionInput{ProjectID: in.ProjectID, Key: in.Key, Evidence: in.Evidence, EvidenceRefs: in.EvidenceRefs, Actor: actor})
			if err != nil {
				return nil, err
			}
			return map[string]any{"key": m.ID, "revision": m.Revision}, nil
		},
	}); err != nil {
		return err
	}
	return nil
}

func milestonePageValue(page service.MilestonePage) (genericActionContinuation, error) {
	items := make([]any, 0, len(page.Milestones))
	for _, view := range page.Milestones {
		items = append(items, milestoneSummaryValue(view))
	}
	result := map[string]any{"items": items}
	return genericActionPageResult(result, page.HasMore, page.NextCursor)
}
