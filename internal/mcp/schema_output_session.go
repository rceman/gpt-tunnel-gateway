package mcp

func sessionRecordSchema() map[string]any {
	sessionID := outputString()
	sessionID["pattern"] = `^S-[0-9ABCDEFGHJKMNPQRSTVWXYZ]{8}$`
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "session_id": sessionID, "project_id": outputString(), "role": outputString(),
		"session_type": outputString(), "session_ref": outputString(), "label": outputString(), "status": outputString(),
		"created_at": outputDateTime(), "started_at": outputDateTime(), "ended_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "schema_version", "session_id", "project_id", "role", "session_type", "status", "created_at", "started_at", "updated_at")
}

func sessionOutputSchema() map[string]any {
	return closedOutput(map[string]any{"action": outputString(), "session": sessionRecordSchema()}, "action", "session")
}
