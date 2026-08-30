package mcp

func runtimeToolOutputSchemas() map[string]map[string]any {
	return map[string]map[string]any{
		"agent_send":          agentSendOutputSchema(),
		"agent_tail":          canonicalAgentTailOutputSchema(),
		"agent_status":        canonicalAgentStatusOutputSchema(),
		"operator_record":     operatorJournalWriteOutputSchema(),
		"operator_history":    operatorJournalHistoryOutputSchema(),
		"operator_checkpoint": operatorJournalWriteOutputSchema(),
		"git_refresh":         closedOutput(map[string]any{"project_id": outputString(), "refreshed": outputBoolean()}, "project_id", "refreshed"),
		"git_refs":            closedOutput(map[string]any{"refs": outputArray(refOutputSchema()), "next_cursor": outputString(), "has_more": outputBoolean()}, "refs", "next_cursor", "has_more"),
		"git_log":             closedOutput(map[string]any{"commits": outputArray(commitOutputSchema()), "next_cursor": outputString(), "has_more": outputBoolean()}, "commits", "next_cursor", "has_more"),
		"git_show":            closedOutput(map[string]any{"text": outputString()}, "text"),
		"git_tree":            closedOutput(map[string]any{"paths": outputArray(outputString()), "next_cursor": outputString(), "has_more": outputBoolean()}, "paths", "next_cursor", "has_more"),
		"git_read_file":       closedOutput(map[string]any{"path": outputString(), "revision": outputString(), "content": outputString()}, "path", "revision", "content"),
		"git_diff":            closedOutput(map[string]any{"diff": outputString()}, "diff"),
		"git_compare":         compareOutputSchema(),
		"git_merge_base":      closedOutput(map[string]any{"merge_base": outputString()}, "merge_base"),
		"git_worktree_status": worktreeStatusOutputSchema(),
		"git_worktree_diff":   closedOutput(map[string]any{"diff": outputString(), "staged": outputBoolean()}, "diff", "staged"),
	}
}
