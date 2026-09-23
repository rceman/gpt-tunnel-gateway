package mcp

import durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"

func sessionInputSchema() map[string]any {
	sessionID := str("Durable session identifier for info, update, or end.")
	sessionID["pattern"] = durableSession.CanonicalSessionIDPattern()
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
