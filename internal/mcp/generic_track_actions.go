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

func trackProperties() map[string]any {
	return map[string]any{
		"key":              outputString(),
		"revision":         outputInteger(),
		"milestone":        boundedADRString("Immutable parent Milestone key.", 8, 128),
		"title":            boundedADRString("Track title.", 3, 128),
		"summary":          boundedADRString("Bounded Track summary.", 0, 256),
		"tasks":            outputArray(boundedADRString("Canonical Task key.", 8, 128)),
		"status":           outputEnum(model.TrackStatuses()...),
		"reason":           boundedADRString("Bounded mutation reason.", 1, 1024),
		"cursor":           outputString(),
		"text":             outputString(),
		"include_archived": outputBoolean(),
	}
}

func trackCreateSchema() map[string]any {
	p := trackProperties()
	return obj(map[string]any{"milestone": p["milestone"], "title": p["title"], "summary": p["summary"], "tasks": p["tasks"]}, "milestone", "title", "tasks")
}

func trackReadSchema() map[string]any {
	p := trackProperties()
	return obj(map[string]any{"key": p["key"], "revision": p["revision"]}, "key")
}

func trackUpdateSchema() map[string]any {
	p := trackProperties()
	return obj(map[string]any{"key": p["key"], "title": p["title"], "summary": p["summary"], "tasks": p["tasks"], "reason": p["reason"]}, "key", "reason")
}

func trackMembershipSchema() map[string]any {
	p := trackProperties()
	return obj(map[string]any{"key": p["key"], "tasks": p["tasks"], "reason": p["reason"]}, "key", "tasks", "reason")
}

func trackCancelSchema() map[string]any {
	p := trackProperties()
	return obj(map[string]any{"key": p["key"], "reason": p["reason"]}, "key", "reason")
}

func trackActionKeySchema() map[string]any {
	return obj(map[string]any{"key": outputString()}, "key")
}

func trackListSchema() map[string]any {
	p := trackProperties()
	return obj(map[string]any{"cursor": p["cursor"], "include_archived": p["include_archived"]})
}

func trackQuerySchema() map[string]any {
	p := trackProperties()
	return obj(map[string]any{"cursor": p["cursor"], "text": p["text"], "status": p["status"], "milestone": p["milestone"]})
}

func trackHistorySchema() map[string]any {
	p := trackProperties()
	return obj(map[string]any{"key": p["key"], "cursor": p["cursor"]}, "key")
}

func trackTaskOutputSchema() map[string]any {
	execution := closedOutput(map[string]any{"status": outputString(), "stage": outputString(), "execution_revision": outputInteger()}, "status")
	return closedOutput(map[string]any{"key": outputString(), "title": outputString(), "priority": outputString(), "dependencies": outputArray(outputString()), "status": outputString(), "execution": execution, "revision": outputInteger()}, "key", "title", "status", "execution", "revision")
}

func trackReviewOutputSchema() map[string]any {
	task := closedOutput(map[string]any{"key": outputString(), "revision": outputInteger(), "revision_sha256": outputString()}, "key", "revision", "revision_sha256")
	return closedOutput(map[string]any{"head": outputString(), "tree": outputString(), "digest": outputString(), "track_revision": outputInteger(), "tasks": outputArray(task), "submitted_at": outputDateTime(), "submitted_by": outputString()}, "head", "tree", "digest", "track_revision", "tasks", "submitted_at", "submitted_by")
}

func trackReadOutputSchema() map[string]any {
	p := trackProperties()
	return closedOutput(map[string]any{"key": p["key"], "revision": p["revision"], "milestone": p["milestone"], "title": p["title"], "summary": p["summary"], "status": p["status"], "tasks": outputArray(trackTaskOutputSchema()), "dispatched_tasks": outputArray(outputString()), "review": trackReviewOutputSchema(), "cancelled_at": outputDateTime(), "cancelled_by": outputString(), "cancelled_reason": outputString(), "created_at": outputDateTime(), "updated_at": outputDateTime()}, "key", "revision", "milestone", "title", "status", "tasks", "created_at")
}

func trackSummaryOutputSchema() map[string]any {
	p := trackProperties()
	return closedOutput(map[string]any{"key": p["key"], "revision": p["revision"], "milestone": p["milestone"], "title": p["title"], "summary": p["summary"], "status": p["status"], "updated_at": outputDateTime()}, "key", "revision", "milestone", "title", "status")
}

func trackMutationOutputSchema() map[string]any {
	return closedOutput(map[string]any{"key": outputString(), "revision": outputInteger()}, "key", "revision")
}

func trackReviewMutationOutputSchema() map[string]any {
	return closedOutput(map[string]any{"key": outputString(), "revision": outputInteger(), "status": outputEnum(model.TrackStatuses()...), "review": trackReviewOutputSchema()}, "key", "revision", "status", "review")
}

func trackListOutputSchema() map[string]any {
	return closedOutput(map[string]any{"items": outputArray(trackSummaryOutputSchema())}, "items")
}

func trackHistoryOutputSchema() map[string]any {
	row := closedOutput(map[string]any{"revision": outputInteger(), "mutation_kind": outputString(), "actor": outputString(), "reason": outputString(), "changed_fields": outputArray(outputString()), "recorded_at": outputDateTime()}, "revision", "mutation_kind", "actor", "reason", "recorded_at")
	return closedOutput(map[string]any{"key": outputString(), "items": outputArray(row)}, "key", "items")
}

func trackReviewValue(review *model.TrackReview) any {
	if review == nil {
		return nil
	}
	tasks := make([]any, 0, len(review.Tasks))
	for _, task := range review.Tasks {
		tasks = append(tasks, map[string]any{"key": task.Key, "revision": task.Revision, "revision_sha256": task.RevisionSHA256})
	}
	return map[string]any{"head": review.Head, "tree": review.Tree, "digest": review.Digest, "track_revision": review.TrackRevision, "tasks": tasks, "submitted_at": review.SubmittedAt, "submitted_by": review.SubmittedBy}
}

func trackViewValue(view service.TrackView) map[string]any {
	track := view.Track
	tasks := make([]any, 0, len(view.Tasks))
	for _, task := range view.Tasks {
		execution := map[string]any{"status": task.Execution.Status}
		if task.Execution.Stage != "" {
			execution["stage"] = task.Execution.Stage
		}
		if task.Execution.ExecutionRevision > 0 {
			execution["execution_revision"] = task.Execution.ExecutionRevision
		}
		value := map[string]any{"key": task.Key, "title": task.Title, "status": task.Status, "execution": execution, "revision": task.Revision}
		if task.Priority != "" {
			value["priority"] = task.Priority
		}
		if len(task.Dependencies) > 0 {
			value["dependencies"] = task.Dependencies
		}
		tasks = append(tasks, value)
	}
	value := map[string]any{"key": track.ID, "revision": track.Revision, "milestone": track.Milestone, "title": track.Title, "status": track.Status, "tasks": tasks, "created_at": track.CreatedAt}
	if track.Summary != "" {
		value["summary"] = track.Summary
	}
	if len(track.DispatchedTasks) > 0 {
		value["dispatched_tasks"] = append([]string{}, track.DispatchedTasks...)
	}
	if track.Review != nil {
		value["review"] = trackReviewValue(track.Review)
	}
	if track.CancelledAt != nil {
		value["cancelled_at"] = *track.CancelledAt
		value["cancelled_by"] = track.CancelledBy
		value["cancelled_reason"] = track.CancelledReason
	}
	if track.Revision > 1 || !track.UpdatedAt.Equal(track.CreatedAt) {
		value["updated_at"] = track.UpdatedAt
	}
	return value
}

func trackSummaryValue(view service.TrackView) map[string]any {
	value := trackViewValue(view)
	delete(value, "tasks")
	delete(value, "dispatched_tasks")
	delete(value, "review")
	delete(value, "cancelled_at")
	delete(value, "cancelled_by")
	delete(value, "cancelled_reason")
	delete(value, "created_at")
	return value
}

func (s *Server) ensureTrackActions() {
	s.trackActions.Do(func() { s.trackActionErr = s.registerTrackActions() })
	if s.trackActionErr != nil {
		panic(s.trackActionErr)
	}
}

func (s *Server) registerTrackActions() error {
	register := func(action GenericAction, role string) error {
		action.AuthorityRole = role
		action.SessionBound = true
		action.LocalReceiptOnly = true
		return s.RegisterGenericAction(action)
	}
	read := func(action GenericAction) error {
		return register(action, actionRoleWorkflow)
	}
	planner := func(action GenericAction) error { return register(action, durableSession.RolePlanner) }
	lead := func(action GenericAction) error { return register(action, durableSession.RoleLead) }
	if err := planner(GenericAction{
		Path:                 "track/create",
		Description:          "Create one planned ordered Track bound to a Milestone.",
		InputSchema:          trackCreateSchema(),
		ExecutionInputSchema: adrExecutionSchema(trackCreateSchema()),
		OutputSchema:         trackMutationOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string   `json:"project_id"`
				Milestone string   `json:"milestone"`
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
			track, result, err := s.Service.TrackLifecycleCreate(ctx, service.TrackCreateInput{ProjectID: in.ProjectID, Milestone: in.Milestone, Title: in.Title, Summary: in.Summary, Tasks: in.Tasks, CreatedBy: actor}, "")
			if err != nil {
				return nil, err
			}
			return map[string]any{"key": track.ID, "revision": result.Revision}, nil
		},
	}); err != nil {
		return err
	}
	if err := read(GenericAction{
		Path:                 "track/read",
		Description:          "Read one ordered Track with live Task projections and review state.",
		InputSchema:          trackReadSchema(),
		ExecutionInputSchema: adrExecutionSchema(trackReadSchema()),
		OutputSchema:         trackReadOutputSchema(),
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
			view, err := s.Service.TrackLifecycleView(ctx, in.ProjectID, in.Key, in.Revision)
			if err != nil {
				return nil, err
			}
			return trackViewValue(view), nil
		},
	}); err != nil {
		return err
	}
	if err := planner(GenericAction{
		Path:                 "track/update",
		Description:          "Update Track metadata or replace ordered membership while planned.",
		InputSchema:          trackUpdateSchema(),
		ExecutionInputSchema: adrExecutionSchema(trackUpdateSchema()),
		OutputSchema:         trackMutationOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string    `json:"project_id"`
				Key       string    `json:"key"`
				Reason    string    `json:"reason"`
				Title     *string   `json:"title,omitempty"`
				Summary   *string   `json:"summary,omitempty"`
				Tasks     *[]string `json:"tasks,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			actor := service.AgentSessionID(ctx)
			if actor == "" {
				return nil, fmt.Errorf("authorized session actor is unavailable")
			}
			track, result, err := s.Service.TrackLifecycleUpdate(ctx, service.TrackUpdateInput{ProjectID: in.ProjectID, Key: in.Key, Title: in.Title, Summary: in.Summary, Tasks: in.Tasks, Actor: actor, Reason: in.Reason})
			if err != nil {
				return nil, err
			}
			return map[string]any{"key": track.ID, "revision": result.Revision}, nil
		},
	}); err != nil {
		return err
	}
	for _, spec := range []struct {
		path, description string
		appendTasks       bool
	}{{"track/append_task", "Append ordered Tasks to a started Track.", true}, {"track/remove_task", "Remove only never-dispatched Tasks from a started Track.", false}} {
		spec := spec
		if err := planner(GenericAction{
			Path:                 spec.path,
			Description:          spec.description,
			InputSchema:          trackMembershipSchema(),
			ExecutionInputSchema: adrExecutionSchema(trackMembershipSchema()),
			OutputSchema:         trackMutationOutputSchema(),
			Annotations: ToolAnnotations{
				DestructiveHint: true,
				IdempotentHint:  true,
			},
			Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var in struct {
					ProjectID string   `json:"project_id"`
					Key       string   `json:"key"`
					Reason    string   `json:"reason"`
					Tasks     []string `json:"tasks"`
				}
				if err := decode(raw, &in); err != nil {
					return nil, err
				}
				actor := service.AgentSessionID(ctx)
				if actor == "" {
					return nil, fmt.Errorf("authorized session actor is unavailable")
				}
				input := service.TrackMembershipInput{ProjectID: in.ProjectID, Key: in.Key, Tasks: in.Tasks, Actor: actor, Reason: in.Reason}
				var track model.Track
				var result service.OperationResult
				var err error
				if spec.appendTasks {
					track, result, err = s.Service.TrackLifecycleAppendTasks(ctx, input)
				} else {
					track, result, err = s.Service.TrackLifecycleRemoveTasks(ctx, input)
				}
				if err != nil {
					return nil, err
				}
				return map[string]any{"key": track.ID, "revision": result.Revision}, nil
			},
		}); err != nil {
			return err
		}
	}
	if err := planner(GenericAction{
		Path:                 "track/cancel",
		Description:          "Cancel one planned Track without deleting its ordered membership.",
		InputSchema:          trackCancelSchema(),
		ExecutionInputSchema: adrExecutionSchema(trackCancelSchema()),
		OutputSchema:         trackMutationOutputSchema(),
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
			track, result, err := s.Service.TrackLifecycleCancel(ctx, service.TrackCancelInput{ProjectID: in.ProjectID, Key: in.Key, Actor: actor, Reason: in.Reason})
			if err != nil {
				return nil, err
			}
			return map[string]any{"key": track.ID, "revision": result.Revision}, nil
		},
	}); err != nil {
		return err
	}
	if err := lead(GenericAction{
		Path:                 "track/submit",
		Description:          "Submit one derived-ready Track for Planner acceptance with a server-bound source snapshot.",
		InputSchema:          trackActionKeySchema(),
		ExecutionInputSchema: adrExecutionSchema(trackActionKeySchema()),
		OutputSchema:         trackReviewMutationOutputSchema(),
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
			result, err := s.Service.TrackLifecycleSubmit(ctx, in.ProjectID, in.Key, actor)
			if err != nil {
				return nil, err
			}
			return map[string]any{"key": result.Track.ID, "revision": result.Track.Revision, "status": result.Track.Status, "review": trackReviewValue(result.Track.Review)}, nil
		},
	}); err != nil {
		return err
	}
	if err := planner(GenericAction{
		Path:                 "track/accept",
		Description:          "Accept one exact fresh Track review snapshot without activation or release side effects.",
		InputSchema:          trackActionKeySchema(),
		ExecutionInputSchema: adrExecutionSchema(trackActionKeySchema()),
		OutputSchema:         trackReviewMutationOutputSchema(),
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
			result, err := s.Service.TrackLifecycleAccept(ctx, in.ProjectID, in.Key, actor)
			if err != nil {
				return nil, err
			}
			return map[string]any{"key": result.Track.ID, "revision": result.Track.Revision, "status": result.Track.Status, "review": trackReviewValue(result.Track.Review)}, nil
		},
	}); err != nil {
		return err
	}
	if err := planner(GenericAction{
		Path:                 "track/archive",
		Description:          "Archive one accepted or cancelled historical Track.",
		InputSchema:          trackCancelSchema(),
		ExecutionInputSchema: adrExecutionSchema(trackCancelSchema()),
		OutputSchema:         trackMutationOutputSchema(),
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
			track, err := s.Service.TrackLifecycleArchive(ctx, in.ProjectID, in.Key, actor, in.Reason)
			if err != nil {
				return nil, err
			}
			return map[string]any{"key": track.ID, "revision": track.Revision}, nil
		},
	}); err != nil {
		return err
	}
	if err := read(GenericAction{
		Path:                 "track/list",
		Description:          "List bounded Track catalogue rows.",
		InputSchema:          trackListSchema(),
		ExecutionInputSchema: adrExecutionSchema(trackListSchema()),
		OutputSchema:         trackListOutputSchema(),
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
			page, err := s.Service.TrackLifecycleListQuery(ctx, in.ProjectID, "", "", "", in.Cursor, in.IncludeArchived)
			if err != nil {
				return nil, err
			}
			return trackPageValue(page)
		},
	}); err != nil {
		return err
	}
	if err := read(GenericAction{
		Path:                 "track/query",
		Description:          "Query bounded Track catalogue rows.",
		InputSchema:          trackQuerySchema(),
		ExecutionInputSchema: adrExecutionSchema(trackQuerySchema()),
		OutputSchema:         trackListOutputSchema(),
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
				Milestone string `json:"milestone,omitempty"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			page, err := s.Service.TrackLifecycleListQuery(ctx, in.ProjectID, in.Text, in.Status, in.Milestone, in.Cursor, in.Status == model.TrackArchived)
			if err != nil {
				return nil, err
			}
			return trackPageValue(page)
		},
	}); err != nil {
		return err
	}
	return read(GenericAction{
		Path:                 "track/history",
		Description:          "Read bounded Track revision and lifecycle history.",
		InputSchema:          trackHistorySchema(),
		ExecutionInputSchema: adrExecutionSchema(trackHistorySchema()),
		OutputSchema:         trackHistoryOutputSchema(),
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
			page, err := s.Service.TrackLifecycleHistory(ctx, in.ProjectID, in.Key, in.Cursor)
			if err != nil {
				return nil, err
			}
			result := map[string]any{"key": in.Key, "items": make([]any, 0, len(page.Records))}
			items := result["items"].([]any)
			for _, record := range page.Records {
				items = append(items, map[string]any{"revision": record.Revision, "mutation_kind": record.MutationKind, "actor": record.Actor, "reason": record.Reason, "changed_fields": record.ChangedFields, "recorded_at": record.RecordedAt})
			}
			result["items"] = items
			cursor := pagination.EncodeOpaqueKeyset("track-history:"+in.ProjectID+":"+in.Key, page.NextCursor)
			return genericActionPageResult(result, page.HasMore, cursor)
		},
	})
}

func trackPageValue(page service.TrackPage) (genericActionContinuation, error) {
	items := make([]any, 0, len(page.Tracks))
	for _, view := range page.Tracks {
		items = append(items, trackSummaryValue(view))
	}
	result := map[string]any{"items": items}
	return genericActionPageResult(result, page.HasMore, page.NextCursor)
}
