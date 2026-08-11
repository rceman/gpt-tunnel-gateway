package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) addRunTools(add toolAdder) {
	limit := integer("Maximum runs", 1, service.MaxPublicCollectionLimit)
	limit["default"] = service.DefaultPublicCollectionLimit
	add("run_list", "List bounded project runs with deterministic continuation. New next_cursor values are compact server-owned tokens of at most 8 safe characters; legacy cursors remain input-compatible.", obj(map[string]any{"project_id": str("Project identifier"), "limit": limit, "cursor": str("Server-owned continuation cursor; new values are <=8 safe characters and legacy values are accepted")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args struct {
			ProjectID string `json:"project_id"`
			Limit     int    `json:"limit,omitempty"`
			Cursor    string `json:"cursor,omitempty"`
		}
		if e := decode(raw, &args); e != nil {
			return nil, e
		}
		v, e := s.Service.RunListPage(ctx, args.ProjectID, service.CollectionPageInput{Limit: args.Limit, Cursor: args.Cursor})
		public := make([]service.PublicRun, 0, len(v.Runs))
		for _, run := range v.Runs {
			public = append(public, service.PublicRunView(run))
		}
		return map[string]any{"runs": public, "next_cursor": v.NextCursor, "has_more": v.HasMore}, e
	})
	add("run_read", "Read one run.", obj(map[string]any{"run_id": str("Run identifier")}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		run, err := s.Service.RunRead(ctx, id)
		if err != nil {
			return nil, err
		}
		return service.PublicRunView(run), nil
	})
	add("run_status", "Alias for run_read.", obj(map[string]any{"run_id": str("Run identifier")}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		run, err := s.Service.RunRead(ctx, id)
		if err != nil {
			return nil, err
		}
		return service.PublicRunView(run), nil
	})
	add("run_report", "Read finalized report.", obj(map[string]any{"run_id": str("Run identifier")}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		return s.Service.RunReport(ctx, id)
	})
	add("run_review_snapshot", "Prepare one bounded structural review snapshot for a run.", obj(map[string]any{"run_id": str("Run identifier")}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		return s.Service.RunReviewSnapshot(ctx, id)
	})
	cursor := str("Server-owned tail cursor; new values are <=8 safe characters and legacy values are accepted")
	cursor["maxLength"] = 4096
	add("run_agent_tail", "Read a bounded incremental tail of the current run's Airelay session.", obj(map[string]any{"run_id": str("Run identifier"), "lines": integer("Number of lines", 1, 200), "cursor": cursor}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		lines, _, e := optionalInteger(raw, "lines")
		if e != nil {
			return nil, e
		}
		result, err := s.Service.RunAgentTailPage(ctx, id, service.AgentTailInput{Lines: lines, Cursor: optionalString(raw, "cursor")})
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	add("run_resume", "Perform one canonical context-compaction recovery for an owned active run.", obj(map[string]any{"run_id": str("Run identifier")}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		return s.Service.RunResume(ctx, id)
	})
	message := str("Bounded message to the registered project session")
	message["minLength"] = 1
	message["maxLength"] = 256
	add("agent_send", "Send one bounded message to the configured project Airelay session.", obj(map[string]any{"project_id": str("Registered project identifier"), "message": message}, "project_id", "message"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		projectID, err := getString(raw, "project_id")
		if err != nil {
			return nil, err
		}
		text, err := getString(raw, "message")
		if err != nil {
			return nil, err
		}
		return s.Service.AgentSend(ctx, projectID, text)
	})
	add("agent_tail", "Read a bounded incremental window from the configured project Airelay session.", agentTailInputSchema(true), func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.agentTailAction(ctx, raw, true)
	})
	add("agent_status", "Read bounded status and capacity warnings from the configured project Airelay session.", obj(map[string]any{"project_id": str("Registered project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		projectID, err := getString(raw, "project_id")
		if err != nil {
			return nil, err
		}
		return s.Service.AgentStatus(ctx, projectID)
	})
	add("run_sweep", "Reprompt or terminalize overdue active runs.", obj(map[string]any{}), func(ctx context.Context, raw json.RawMessage) (any, error) { return s.Service.RunSweep(ctx) })
	add("run_cancel", "Request cooperative cancellation through Airelay.", obj(map[string]any{"run_id": str("Run identifier"), "expected_hub_revision": str("Optimistic hub revision")}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		return s.Service.RunCancel(ctx, id, optionalString(raw, "expected_hub_revision"))
	})
	add("run_cancel_acknowledge_no_mutation", "Acknowledge delivered cancellation and terminalize only when the configured task worktree is clean at its immutable base; this does not send a cancellation or hard-interrupt Airelay.", obj(map[string]any{"run_id": str("Run identifier"), "expected_hub_revision": str("Optimistic hub revision")}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		return s.Service.RunCancelAcknowledgeNoMutation(ctx, id, optionalString(raw, "expected_hub_revision"))
	})
}

func agentTailInputSchema(legacySkip bool) map[string]any {
	cursor := str("Server-owned tail cursor; new values are <=8 safe characters and legacy values are accepted")
	cursor["maxLength"] = 4096
	properties := map[string]any{
		"project_id": str("Registered project identifier"),
		"lines":      integer("Number of lines", 1, 200),
		"cursor":     cursor,
	}
	if legacySkip {
		properties["skip"] = integer("Newest lines to skip", 0, 196)
	}
	return obj(properties, "project_id")
}

func (s *Server) agentTailAction(ctx context.Context, raw json.RawMessage, legacySkip bool) (any, error) {
	projectID, err := getString(raw, "project_id")
	if err != nil {
		return nil, err
	}
	lines, _, err := optionalInteger(raw, "lines")
	if err != nil {
		return nil, err
	}
	skip := 0
	if legacySkip {
		skip, _, err = optionalInteger(raw, "skip")
		if err != nil {
			return nil, err
		}
	}
	return s.Service.AgentTailPage(ctx, projectID, service.AgentTailInput{
		Lines:  lines,
		Skip:   skip,
		Cursor: optionalString(raw, "cursor"),
	})
}
