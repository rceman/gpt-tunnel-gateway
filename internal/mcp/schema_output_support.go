package mcp

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
	return map[string]any{
		"type": "object",
		"oneOf": []any{
			obj(map[string]any{"action": map[string]any{"type": "string", "const": "start"}, "project_id": str("Registered project identifier for start."), "role": str("Server-authorized session role."), "session_type": str("Session type."), "session_ref": str("Optional caller reference."), "label": str("Optional bounded session label.")}, "action", "project_id", "role", "session_type"),
			obj(map[string]any{"action": map[string]any{"type": "string", "const": "list"}}, "action"),
			obj(map[string]any{"action": map[string]any{"type": "string", "const": "info"}, "session_id": sessionID}, "action", "session_id"),
			obj(map[string]any{"action": map[string]any{"type": "string", "const": "update"}, "session_id": sessionID, "session_ref": str("Optional caller reference."), "label": str("Optional bounded session label.")}, "action", "session_id"),
			obj(map[string]any{"action": map[string]any{"type": "string", "const": "end"}, "session_id": sessionID}, "action", "session_id"),
		},
		"additionalProperties": false,
	}
}
