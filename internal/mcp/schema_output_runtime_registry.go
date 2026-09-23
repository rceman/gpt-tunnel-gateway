package mcp

func paginatedCollectionOutputSchema(result map[string]any) map[string]any {
	pagination := closedOutput(map[string]any{"next_cursor": publicServerCursorSchema()}, "next_cursor")
	return closedOutput(map[string]any{"result": result, "pagination": pagination}, "result")
}

func runtimeToolOutputSchemas() map[string]map[string]any {
	return map[string]map[string]any{
		"git_refresh":         closedOutput(map[string]any{"project_id": outputString(), "refreshed": outputBoolean()}, "project_id", "refreshed"),
		"git_refs":            paginatedCollectionOutputSchema(closedOutput(map[string]any{"refs": outputArray(refOutputSchema())}, "refs")),
		"git_log":             paginatedCollectionOutputSchema(closedOutput(map[string]any{"commits": outputArray(commitOutputSchema())}, "commits")),
		"git_show":            closedOutput(map[string]any{"text": outputString()}, "text"),
		"git_tree":            paginatedCollectionOutputSchema(closedOutput(map[string]any{"paths": outputArray(outputString())}, "paths")),
		"git_read_file":       closedOutput(map[string]any{"path": outputString(), "revision": publicGitRevisionOrRefOutputSchema(), "content": outputString()}, "path", "revision", "content"),
		"git_diff":            closedOutput(map[string]any{"diff": outputString()}, "diff"),
		"git_compare":         compareOutputSchema(),
		"git_merge_base":      closedOutput(map[string]any{"merge_base": publicGitFingerprintOutputSchema()}, "merge_base"),
		"git_worktree_status": worktreeStatusOutputSchema(),
		"git_worktree_diff":   closedOutput(map[string]any{"diff": outputString(), "staged": outputBoolean()}, "diff", "staged"),
	}
}
