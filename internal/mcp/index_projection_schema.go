package mcp

func gitLogRowOutputSchema() map[string]any {
	short := outputString()
	short["pattern"] = "^[0-9a-f]{10}$"
	return closedOutput(map[string]any{
		"short_sha": short,
		"commit":    outputString(),
		"date":      outputDateTime(),
	}, "short_sha", "commit", "date")
}

func taskIndexRowOutputSchema() map[string]any {
	nullableString := map[string]any{"anyOf": []any{outputString(), map[string]any{"type": "null"}}}
	return closedOutput(map[string]any{
		"id":                outputString(),
		"title":             outputString(),
		"status":            outputString(),
		"updated_at":        outputDateTime(),
		"latest_run_id":     nullableString,
		"latest_run_status": nullableString,
	}, "id", "title", "status", "updated_at", "latest_run_id", "latest_run_status")
}
