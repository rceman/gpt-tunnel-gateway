package mcp

import "github.com/rceman/gpt-tunnel-gateway/internal/mcpmanifest"

func readOnlyAnnotations() ToolAnnotations {
	return ToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: false,
		IdempotentHint:  true,
		OpenWorldHint:   false,
	}
}

// canonicalToolManifest is the single stable public MCP transport inventory. Legacy
// handlers remain internal action registrations for schema/call resolution.
var canonicalToolManifest = mcpmanifest.CanonicalToolNames()

func canonicalToolNames() []string { return append([]string{}, canonicalToolManifest...) }
func additiveExternalAnnotations() ToolAnnotations {
	return ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: false,
		IdempotentHint:  false,
		OpenWorldHint:   true,
	}
}
func idempotentMutationAnnotations() ToolAnnotations {
	return ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: false,
		IdempotentHint:  true,
		OpenWorldHint:   true,
	}
}
func destructiveExternalAnnotations() ToolAnnotations {
	return ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: true,
		IdempotentHint:  false,
		OpenWorldHint:   true,
	}
}

var toolAnnotations = func() map[string]ToolAnnotations {
	result := map[string]ToolAnnotations{}
	result["schema"] = readOnlyAnnotations()
	result["guide"] = readOnlyAnnotations()
	result["projects"] = readOnlyAnnotations()
	result["session_start"] = additiveExternalAnnotations()
	result["session_update"] = idempotentMutationAnnotations()
	result["call"] = additiveExternalAnnotations()
	result["status"] = readOnlyAnnotations()
	result["rules"] = readOnlyAnnotations()
	result["session"] = destructiveExternalAnnotations()
	for _, name := range []string{
		"system_ping", "gateway_capabilities",
		"adr_list", "adr_read", "task_list", "task_read",
		"task_revision_list", "task_revision_read",
		"git_refs", "git_log", "git_show", "git_tree", "git_read_file", "git_diff", "git_compare",
		"git_merge_base", "git_worktree_status", "git_worktree_diff",
	} {
		result[name] = readOnlyAnnotations()
	}
	result["operator_record"] = additiveExternalAnnotations()
	result["operator_checkpoint"] = additiveExternalAnnotations()
	result["operator_history"] = readOnlyAnnotations()
	for _, name := range []string{"adr_create", "task_create"} {
		result[name] = additiveExternalAnnotations()
	}
	result["task_correction_create"] = additiveExternalAnnotations()
	for _, name := range []string{"task_supersede"} {
		result[name] = destructiveExternalAnnotations()
	}
	result["git_refresh"] = ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: false,
		IdempotentHint:  true,
		OpenWorldHint:   true,
	}
	return result
}()
