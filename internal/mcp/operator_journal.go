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

func operatorJournalBoundedString(base map[string]any, max int) map[string]any {
	base["minLength"] = 1
	base["maxLength"] = max
	return base
}

func operatorJournalNullableString(base map[string]any) map[string]any {
	base["type"] = []any{"string", "null"}
	base["minLength"] = 1
	base["maxLength"] = model.MaxOperatorSessionIDBytes
	return base
}

func operatorJournalProjectID(base map[string]any) map[string]any {
	base["pattern"] = "^[a-z0-9][a-z0-9_-]{0,63}$"
	base["minLength"] = 1
	base["maxLength"] = 64
	return base
}

func operatorJournalEventID(base map[string]any) map[string]any {
	base["pattern"] = model.OperatorEventIDPattern
	base["minLength"] = 6
	base["maxLength"] = 21
	return base
}

func operatorJournalObjectID(base map[string]any) map[string]any {
	base["pattern"] = "^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$"
	base["minLength"] = 1
	base["maxLength"] = 128
	return base
}

func operatorJournalCommit(base map[string]any) map[string]any {
	base["pattern"] = "^[0-9a-f]{40}$"
	base["minLength"] = 40
	base["maxLength"] = 40
	return base
}

func operatorJournalADR(base map[string]any) map[string]any {
	return map[string]any{"anyOf": []any{
		map[string]any{"type": "string", "minLength": 5, "maxLength": 68, "pattern": "^ADR-[A-Za-z0-9][A-Za-z0-9._-]{0,63}$"},
		map[string]any{"type": "string", "minLength": 6, "maxLength": 21, "pattern": model.OperatorCompactADRPattern},
	}}
}

func operatorJournalArray(items map[string]any, max int) map[string]any {
	return map[string]any{"type": "array", "maxItems": max, "items": items}
}

func operatorJournalContentInputSchema() map[string]any {
	item := operatorJournalBoundedString(str("Bounded concise journal entry"), model.MaxOperatorContentItemBytes)
	return obj(map[string]any{
		"decisions": operatorJournalArray(item, model.MaxOperatorContentItems), "commitments": operatorJournalArray(item, model.MaxOperatorContentItems), "facts": operatorJournalArray(item, model.MaxOperatorContentItems),
		"assumptions": operatorJournalArray(item, model.MaxOperatorContentItems), "blockers": operatorJournalArray(item, model.MaxOperatorContentItems), "unresolved": operatorJournalArray(item, model.MaxOperatorContentItems), "next_actions": operatorJournalArray(item, model.MaxOperatorContentItems),
	}, "decisions", "commitments", "facts", "assumptions", "blockers", "unresolved", "next_actions")
}

func operatorJournalReferencesInputSchema() map[string]any {
	return obj(map[string]any{
		"plan_sections": operatorJournalArray(operatorJournalObjectID(str("Bounded plan section identifier")), model.MaxOperatorReferenceItems),
		"adrs":          operatorJournalArray(operatorJournalADR(str("Bounded ADR reference")), model.MaxOperatorReferenceItems),
		"tasks":         operatorJournalArray(operatorJournalObjectID(str("Bounded task identifier")), model.MaxOperatorReferenceItems),
		"runs":          operatorJournalArray(operatorJournalObjectID(str("Bounded run identifier")), model.MaxOperatorReferenceItems),
		"commits":       operatorJournalArray(operatorJournalCommit(str("Git commit SHA")), model.MaxOperatorReferenceItems),
		"identities":    operatorJournalArray(operatorJournalBoundedString(str("Bounded operator identity reference"), model.MaxOperatorContentItemBytes), model.MaxOperatorReferenceItems),
	}, "plan_sections", "adrs", "tasks", "runs", "commits", "identities")
}

func operatorRecordInputSchema() map[string]any {
	kind := outputEnum(string(model.OperatorUserTalk), string(model.OperatorReasoningSummary), string(model.OperatorTaskPlan), string(model.OperatorTaskReview), string(model.OperatorCorrection))
	return obj(map[string]any{
		"project_id": operatorJournalProjectID(str("Project identifier")), "session_id": operatorJournalNullableString(str("Nullable bounded session identifier")),
		"kind": kind, "summary": operatorJournalBoundedString(str("Concise journal summary"), model.MaxOperatorSummaryBytes), "content": operatorJournalContentInputSchema(),
		"references": operatorJournalReferencesInputSchema(), "supersedes_event_id": operatorJournalEventID(str("Existing same-project event for a correction")),
		"occurred_at": map[string]any{"type": "string", "format": "date-time", "description": "Optional strict RFC3339 occurrence time"},
		"actor":       operatorJournalBoundedString(str("Bounded operator identity"), model.MaxOperatorActorBytes), "expected_hub_revision": str("Optimistic hub revision"),
	}, "project_id", "kind", "summary", "content", "references", "actor")
}

func operatorCheckpointInputSchema() map[string]any {
	return obj(map[string]any{
		"project_id": operatorJournalProjectID(str("Project identifier")), "session_id": operatorJournalNullableString(str("Nullable bounded session identifier")),
		"summary": operatorJournalBoundedString(str("Concise checkpoint summary"), model.MaxOperatorSummaryBytes), "content": operatorJournalContentInputSchema(),
		"references": operatorJournalReferencesInputSchema(), "occurred_at": map[string]any{"type": "string", "format": "date-time", "description": "Optional strict RFC3339 occurrence time"},
		"actor": operatorJournalBoundedString(str("Bounded operator identity"), model.MaxOperatorActorBytes), "expected_hub_revision": str("Optimistic hub revision"),
	}, "project_id", "summary", "content", "references", "actor")
}

func operatorHistoryInputSchema() map[string]any {
	kind := outputEnum(string(model.OperatorUserTalk), string(model.OperatorReasoningSummary), string(model.OperatorTaskPlan), string(model.OperatorTaskReview), string(model.OperatorOperation), string(model.OperatorCheckpoint), string(model.OperatorCorrection))
	return obj(map[string]any{
		"project_id": operatorJournalProjectID(str("Project identifier")), "after_event_id": operatorJournalEventID(str("Exclusive project-scoped event cursor")),
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
