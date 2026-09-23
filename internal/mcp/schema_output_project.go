package mcp

import "github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"

func outputString() map[string]any  { return map[string]any{"type": "string"} }
func outputBoolean() map[string]any { return map[string]any{"type": "boolean"} }
func outputInteger() map[string]any { return map[string]any{"type": "integer"} }
func outputDateTime() map[string]any {
	return map[string]any{"type": "string", "format": "date-time"}
}
func publicGitFingerprintOutputSchema() map[string]any {
	return map[string]any{"type": "string", "minLength": 8, "maxLength": 8, "pattern": "^[0-9a-f]{8}$"}
}
func publicServerCursorSchema() map[string]any {
	return map[string]any{"type": "string", "minLength": 8, "maxLength": 8, "pattern": `^[ABCDEFGHJKMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789]{8}$`}
}
func publicGitFingerprintOrEmptyOutputSchema() map[string]any {
	return map[string]any{"anyOf": []any{publicGitFingerprintOutputSchema(), map[string]any{"type": "string", "const": ""}}}
}
func publicGitFingerprintExclusionSchema() map[string]any {
	return map[string]any{"anyOf": []any{
		map[string]any{"type": "string", "pattern": `^[0-9a-fA-F]{9,}$`},
		map[string]any{"type": "string", "pattern": `^[0-9a-fA-F]{8}$`, "not": map[string]any{"type": "string", "pattern": `^[0-9a-f]{8}$`}},
	}}
}

func publicGitRevisionOrRefOutputSchema() map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": 512, "not": publicGitFingerprintExclusionSchema()}
}
func outputArray(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}
func outputEnum(values ...string) map[string]any {
	items := make([]any, 0, len(values))
	for _, value := range values {
		items = append(items, value)
	}
	return map[string]any{"type": "string", "enum": items}
}
func closedOutput(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func projectOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "repository_url": outputString(),
		"default_branch": outputString(), "workflow_repository": outputString(), "workflow_commit": publicGitFingerprintOrEmptyOutputSchema(),
		"status": outputString(), "active_task_id": outputString(),
		"created_at": outputDateTime(), "updated_at": outputDateTime(),
	}, "schema_version", "id", "repository_url", "default_branch", "workflow_repository", "workflow_commit", "status", "created_at", "updated_at")
}

func planOutputSchema() map[string]any {
	sectionIndex := closedOutput(map[string]any{
		"id": outputString(), "title": outputString(), "short_description": outputString(), "revision": outputInteger(),
	}, "id", "title", "short_description", "revision")
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "project_id": outputString(), "revision": outputInteger(),
		"title": outputString(), "summary": outputString(), "current_objective": outputString(), "queue": outputArray(outputString()), "sections": outputArray(sectionIndex),
		"active_task_id": outputString(),
		"updated_by":     outputString(), "updated_at": outputDateTime(),
	}, "schema_version", "project_id", "revision", "title", "summary", "current_objective", "queue", "sections", "updated_by", "updated_at")
}

func planStatusOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "project_id": outputString(), "revision": outputInteger(),
		"title": outputString(), "summary": outputString(), "current_objective": outputString(), "queue": outputArray(outputString()), "sections": outputArray(outputString()),
		"active_task_id": outputString(), "updated_by": outputString(), "updated_at": outputDateTime(),
	}, "schema_version", "project_id", "revision", "title", "summary", "current_objective", "queue", "sections", "updated_by", "updated_at")
}

func planSectionOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "project_id": outputString(), "id": outputString(), "revision": outputInteger(),
		"title": outputString(), "short_description": outputString(), "description": outputString(), "updated_by": outputString(), "updated_at": outputDateTime(),
	}, "schema_version", "project_id", "id", "revision", "title", "short_description", "description", "updated_by", "updated_at")
}

func planRenderOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "project_id": outputString(), "revision": outputInteger(), "title": outputString(),
		"summary": outputString(), "current_objective": outputString(), "text": outputString(),
	}, "schema_version", "project_id", "revision", "title", "summary", "current_objective", "text")
}

func relationGroupedOutputSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "object", "additionalProperties": outputString()}}
}

func adrOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"key": outputString(), "revision": outputInteger(), "title": outputString(), "summary": outputString(), "status": outputEnum(sqlitestore.SharedLifecycleStatusValues("adr", false)...),
		"context": outputString(), "decision": outputString(), "consequences": outputString(), "created_at": outputDateTime(),
		"updated_at": outputDateTime(), "revision_reason": outputString(), "relations": relationGroupedOutputSchema(),
	}, "key", "revision", "title", "status", "context", "decision", "consequences", "created_at", "relations")
}

func taskOutputSchema() map[string]any {
	strings := outputArray(outputString())
	return closedOutput(map[string]any{
		"schema_version": outputInteger(), "id": outputString(), "project_id": outputString(),
		"title": outputString(), "type": outputEnum("task", "bug", "perf", "chore"), "objective": outputString(), "branch": outputString(), "base_revision": publicGitFingerprintOrEmptyOutputSchema(),
		"acceptance_criteria": strings, "constraints": strings, "required_gates": strings,
		"workflow_policy_revision": outputInteger(), "effective_ci_field": outputString(), "effective_ci_mode": outputString(), "wait_for_ci": outputBoolean(), "ci_blocking": outputBoolean(), "agent_may_wait": outputBoolean(),
		"status": outputString(), "supersedes": outputString(), "created_by": outputString(), "created_at": outputDateTime(),
	}, "schema_version", "id", "project_id", "title", "objective", "branch", "acceptance_criteria", "constraints", "status", "created_by", "created_at")
}
