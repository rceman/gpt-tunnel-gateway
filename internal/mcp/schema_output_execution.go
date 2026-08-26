package mcp

import (
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func transactionOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"before": outputString(), "after": outputString(), "remote": outputString(), "branch": outputString(), "paths": outputArray(outputString()),
	}, "before", "after", "remote", "branch", "paths")
}

func operationOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"hub": transactionOutputSchema(), "project_id": outputString(), "task_id": outputString(), "status": outputString(),
	}, "hub", "status")
}

func durableMutationReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "status": outputString(), "operation": operationOutputSchema(), "error": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}

func taskWorkReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "status": outputString(), "result": map[string]any{"type": "object", "additionalProperties": true}, "error": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}

func taskFinalizeReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "status": outputString(), "result": map[string]any{"type": "object", "additionalProperties": true}, "error": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}

func agentMutationReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "status": outputString(), "agent": agentObjectOutputSchema(), "operation": operationOutputSchema(), "error": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}

func agentIPCReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "status": outputString(), "result": map[string]any{"type": "object", "additionalProperties": true}, "error": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}

func watcherNudgeReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "status": outputString(), "result": map[string]any{"type": "object", "additionalProperties": true}, "error": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}

func watcherGuideMutationReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "status": outputString(), "guide": watcherObjectOutputSchema(), "operation": operationOutputSchema(), "error": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}

func projectConfigurationMutationReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "status": outputString(), "configuration": projectConfigurationObjectSchema(), "operation": operationOutputSchema(), "error": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}

func projectRemoveReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "status": outputString(), "result": map[string]any{"type": "object", "additionalProperties": true}, "error": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}

func taskSupersedeReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "status": outputString(), "task": taskOutputSchema(), "operation": operationOutputSchema(), "error": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "operation_id", "status", "created_at", "updated_at")
}

func operatorJournalEventOutputSchema() map[string]any {
	contentItem := operatorJournalBoundedString(outputString(), model.MaxOperatorContentItemBytes)
	content := closedOutput(map[string]any{
		"decisions": operatorJournalArray(contentItem, model.MaxOperatorContentItems), "commitments": operatorJournalArray(contentItem, model.MaxOperatorContentItems), "facts": operatorJournalArray(contentItem, model.MaxOperatorContentItems),
		"assumptions": operatorJournalArray(contentItem, model.MaxOperatorContentItems), "blockers": operatorJournalArray(contentItem, model.MaxOperatorContentItems), "unresolved": operatorJournalArray(contentItem, model.MaxOperatorContentItems), "next_actions": operatorJournalArray(contentItem, model.MaxOperatorContentItems),
	}, "decisions", "commitments", "facts", "assumptions", "blockers", "unresolved", "next_actions")
	references := closedOutput(map[string]any{
		"plan_sections": operatorJournalArray(operatorJournalObjectID(outputString()), model.MaxOperatorReferenceItems), "adrs": operatorJournalArray(operatorJournalADR(outputString()), model.MaxOperatorReferenceItems), "tasks": operatorJournalArray(operatorJournalObjectID(outputString()), model.MaxOperatorReferenceItems),
		"runs": operatorJournalArray(operatorJournalObjectID(outputString()), model.MaxOperatorReferenceItems), "commits": operatorJournalArray(operatorJournalCommit(outputString()), model.MaxOperatorReferenceItems), "identities": operatorJournalArray(operatorJournalBoundedString(outputString(), model.MaxOperatorContentItemBytes), model.MaxOperatorReferenceItems),
	}, "plan_sections", "adrs", "tasks", "runs", "commits", "identities")
	sessionID := operatorJournalNullableString(outputString())
	kind := outputEnum("user_talk", "reasoning_summary", "task_plan", "task_review", "operation", "checkpoint", "correction")
	event := closedOutput(map[string]any{
		"schema_version": func() map[string]any {
			value := outputInteger()
			value["const"] = float64(model.OperatorJournalSchemaVersion)
			return value
		}(), "id": operatorJournalEventID(outputString()), "project_id": operatorJournalProjectID(outputString()), "session_id": sessionID,
		"kind": kind, "summary": operatorJournalBoundedString(outputString(), model.MaxOperatorSummaryBytes), "content": content, "references": references,
		"supersedes_event_id": operatorJournalEventID(outputString()), "actor": operatorJournalBoundedString(outputString(), model.MaxOperatorActorBytes), "occurred_at": outputDateTime(), "recorded_at": outputDateTime(),
	}, "schema_version", "id", "project_id", "session_id", "kind", "summary", "content", "references", "actor", "occurred_at", "recorded_at")
	event["allOf"] = []any{
		map[string]any{"if": map[string]any{"properties": map[string]any{"kind": map[string]any{"const": "correction"}}, "required": []any{"kind"}}, "then": map[string]any{"required": []any{"supersedes_event_id"}}},
		map[string]any{"if": map[string]any{"required": []any{"supersedes_event_id"}}, "then": map[string]any{"properties": map[string]any{"kind": map[string]any{"const": "correction"}}}},
	}
	return event
}

func operatorJournalWriteOutputSchema() map[string]any {
	return closedOutput(map[string]any{"event": operatorJournalEventOutputSchema(), "operation": sharedOperationOutputSchema()}, "event", "operation")
}

func sharedOperationOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"operation_id": outputString(), "project_id": outputString(), "status": outputString(),
	}, "operation_id", "project_id", "status")
}

func operatorJournalHistoryOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"project_id": operatorJournalProjectID(outputString()), "events": operatorJournalArray(operatorJournalEventOutputSchema(), model.MaxOperatorHistoryLimit), "hub_revision": outputString(),
		"has_more": outputBoolean(), "next_after_event_id": operatorJournalEventID(outputString()),
	}, "project_id", "events", "hub_revision", "has_more")
}

func projectIdentifiersOutputSchema() map[string]any {
	schemaVersion := outputInteger()
	schemaVersion["const"] = float64(1)
	projectID := outputString()
	projectID["pattern"] = "^[a-z0-9][a-z0-9_-]{0,63}$"
	projectID["minLength"] = 1
	projectID["maxLength"] = 64
	projectCode := outputString()
	projectCode["pattern"] = "^[A-Z]{3}$"
	number := map[string]any{"type": "integer", "minimum": 1, "maximum": 9007199254740991}
	return closedOutput(map[string]any{
		"schema_version": schemaVersion, "project_id": projectID, "project_code": projectCode,
		"next_task_number": number, "next_adr_number": number,
	}, "schema_version", "project_id", "project_code", "next_task_number", "next_adr_number")
}

func agentSendOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"project_id": outputString(), "delivered": outputBoolean(), "exit_code": outputInteger(),
		"stdout": outputString(), "stderr": outputString(), "started_at": outputDateTime(), "finished_at": outputDateTime(), "error": outputString(),
	}, "project_id", "delivered", "exit_code", "stdout", "stderr", "started_at", "finished_at")
}

func agentTailOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"lines": outputArray(outputString()), "count": outputInteger(), "has_new_info": outputBoolean(), "overflow": outputBoolean(), "history_truncated": outputBoolean(),
	}, "lines", "count", "has_new_info", "overflow", "history_truncated")
}

func agentStatusOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"project_id": outputString(), "state": outputEnum("idle", "running", "waiting_for_input", "compacting", "compacted_resuming", "compacted_idle", "capacity_blocked", "rate_limited", "completion_pending", "finalization_pending", "stalled", "error", "unknown"), "controller_reachable": outputBoolean(),
		"airelay_version": outputString(), "protocol_version": outputString(), "capacity_warnings": outputArray(outputString()),
		"exit_code": outputInteger(), "error": outputString(),
	}, "project_id", "state", "controller_reachable", "capacity_warnings", "exit_code")
}
