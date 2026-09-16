package mcp

import durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"

func refOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"name": outputString(), "object_type": outputString(), "object_name": outputString(), "subject": outputString(), "committer_date": outputString(),
	}, "name", "object_type", "object_name")
}

func commitOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"sha": outputString(), "parents": outputArray(outputString()), "author_name": outputString(), "author_email": outputString(),
		"author_date": outputString(), "subject": outputString(),
	}, "sha", "parents", "author_name", "author_email", "author_date", "subject")
}

func compareOutputSchema() map[string]any {
	return closedOutput(map[string]any{"merge_base": outputString(), "left_only": outputInteger(), "right_only": outputInteger()}, "merge_base", "left_only", "right_only")
}

func sessionInputSchema() map[string]any {
	sessionID := str("Durable session identifier for info, update, or end.")
	sessionID["pattern"] = sessionIDPattern
	agent := str("Logical Agent key for server-side managed-role binding.")
	agent["minLength"] = 1
	agent["maxLength"] = 128
	label := str("Optional bounded durable Session label.")
	label["maxLength"] = 256
	start := obj(map[string]any{"action": map[string]any{"type": "string", "const": "start"}, "project_id": str("Registered project identifier for start."), "role": durableSession.WorkflowRoleSchema("Server-authorized session role."), "session_type": str("Session type."), "agent": agent, "label": label}, "action", "project_id", "role", "session_type")
	start["if"] = map[string]any{
		"properties": map[string]any{"role": map[string]any{"enum": []any{durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker}}},
		"required":   []string{"role"},
	}
	start["then"] = map[string]any{"required": []string{"agent"}}
	return map[string]any{
		"type": "object",
		"oneOf": []any{
			start,
			obj(map[string]any{"action": map[string]any{"type": "string", "const": "list"}}, "action"),
			obj(map[string]any{"action": map[string]any{"type": "string", "const": "info"}, "session_id": sessionID}, "action", "session_id"),
			obj(map[string]any{"action": map[string]any{"type": "string", "const": "update"}, "session_id": sessionID, "agent": agent, "label": label}, "action", "session_id"),
			obj(map[string]any{"action": map[string]any{"type": "string", "const": "end"}, "session_id": sessionID}, "action", "session_id"),
		},
		"additionalProperties": false,
	}
}
