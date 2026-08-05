package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func nullableString(desc string) map[string]any {
	return map[string]any{"type": []any{"string", "null"}, "description": desc}
}

func operatorJournalContentInputSchema() map[string]any {
	item := str("Bounded concise journal entry")
	return obj(map[string]any{
		"decisions": array(item), "commitments": array(item), "facts": array(item),
		"assumptions": array(item), "blockers": array(item), "unresolved": array(item), "next_actions": array(item),
	}, "decisions", "commitments", "facts", "assumptions", "blockers", "unresolved", "next_actions")
}

func operatorJournalReferencesInputSchema() map[string]any {
	item := str("Bounded durable object identifier")
	return obj(map[string]any{
		"plan_sections": array(item), "adrs": array(item), "tasks": array(item),
		"runs": array(item), "commits": array(item), "identities": array(item),
	}, "plan_sections", "adrs", "tasks", "runs", "commits", "identities")
}

func operatorRecordInputSchema() map[string]any {
	kind := outputEnum(string(model.OperatorUserTalk), string(model.OperatorReasoningSummary), string(model.OperatorTaskPlan), string(model.OperatorTaskReview), string(model.OperatorCorrection))
	return obj(map[string]any{
		"project_id": str("Project identifier"), "session_id": nullableString("Nullable bounded session identifier"),
		"kind": kind, "summary": str("Concise journal summary"), "content": operatorJournalContentInputSchema(),
		"references": operatorJournalReferencesInputSchema(), "supersedes_event_id": str("Existing same-project event for a correction"),
		"occurred_at": map[string]any{"type": "string", "format": "date-time", "description": "Optional strict RFC3339 occurrence time"},
		"actor":       str("Bounded operator identity"), "expected_hub_revision": str("Optimistic hub revision"),
	}, "project_id", "kind", "summary", "content", "references", "actor")
}

func operatorCheckpointInputSchema() map[string]any {
	return obj(map[string]any{
		"project_id": str("Project identifier"), "session_id": nullableString("Nullable bounded session identifier"),
		"summary": str("Concise checkpoint summary"), "content": operatorJournalContentInputSchema(),
		"references": operatorJournalReferencesInputSchema(), "occurred_at": map[string]any{"type": "string", "format": "date-time", "description": "Optional strict RFC3339 occurrence time"},
		"actor": str("Bounded operator identity"), "expected_hub_revision": str("Optimistic hub revision"),
	}, "project_id", "summary", "content", "references", "actor")
}

func operatorHistoryInputSchema() map[string]any {
	kind := outputEnum(string(model.OperatorUserTalk), string(model.OperatorReasoningSummary), string(model.OperatorTaskPlan), string(model.OperatorTaskReview), string(model.OperatorOperation), string(model.OperatorCheckpoint), string(model.OperatorCorrection))
	return obj(map[string]any{
		"project_id": str("Project identifier"), "after_event_id": str("Exclusive project-scoped event cursor"),
		"kind": kind, "limit": integer("Maximum events", 1, model.MaxOperatorHistoryLimit),
	}, "project_id")
}

func addOperatorJournalTools(add func(string, string, map[string]any, func(context.Context, json.RawMessage) (any, error)), s *Server) {
	add("operator_record", "Append one concise owner/operator context record to the immutable project journal.", operatorRecordInputSchema(), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.OperatorRecordInput
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		event, operation, err := s.Service.OperatorRecord(ctx, in)
		if err != nil {
			return nil, err
		}
		return map[string]any{"event": event, "operation": operation}, nil
	})
	add("operator_history", "Read bounded numeric chronological operator journal history.", operatorHistoryInputSchema(), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.OperatorHistoryInput
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		return s.Service.OperatorHistory(ctx, in)
	})
	add("operator_checkpoint", "Append one explicit handoff checkpoint to the immutable operator journal.", operatorCheckpointInputSchema(), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.OperatorCheckpointInput
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		event, operation, err := s.Service.OperatorCheckpoint(ctx, in)
		if err != nil {
			return nil, err
		}
		return map[string]any{"event": event, "operation": operation}, nil
	})
}
