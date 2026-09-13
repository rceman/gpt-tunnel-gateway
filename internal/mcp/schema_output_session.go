package mcp

import durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"

func sessionRecordSchema() map[string]any {
	sessionID := outputString()
	sessionID["pattern"] = sessionIDPattern
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "session_id": sessionID, "project_id": outputString(), "project_code": outputString(), "role": durableSession.WorkflowRoleOutputSchema(),
		"session_type": outputString(), "session_ref": outputString(), "label": outputString(), "status": outputString(),
		"created_at": outputDateTime(), "started_at": outputDateTime(), "ended_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "schema_version", "session_id", "project_id", "role", "session_type", "status", "created_at", "started_at", "updated_at")
}

func sessionOutputSchema() map[string]any {
	listItem := closedOutput(map[string]any{
		"session_id": sessionIDOutputSchema(), "role": outputString(), "project_id": outputString(),
		"ref": map[string]any{"anyOf": []any{outputString(), map[string]any{"type": "null"}}},
	}, "session_id", "role", "project_id", "ref")
	list := closedOutput(map[string]any{"action": outputString(), "sessions": outputArray(listItem)}, "action", "sessions")
	mutation := closedOutput(map[string]any{"action": outputString(), "session": sessionRecordSchema()}, "action", "session")
	return map[string]any{"type": "object", "oneOf": []any{mutation, list}}
}

func sessionIDOutputSchema() map[string]any {
	id := outputString()
	id["pattern"] = sessionIDPattern
	return id
}

const (
	sessionIDPattern      = `^[A-Z]{3}_[A-Z]{3}_[PLAW]_[a-z0-9]{5}$`
	agentSessionIDPattern = sessionIDPattern
)
