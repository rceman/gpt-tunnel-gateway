package mcp

import "github.com/rceman/gpt-tunnel-gateway/internal/model"

func taskLifecycleOutputSchema() map[string]any {
	return closedOutput(map[string]any{"key": outputString(), "revision": outputInteger()}, "key", "revision")
}

func taskLifecycleInputProperties() map[string]any {
	return map[string]any{"type": taskTypeSchema(), "scope": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"files": outputArray(outputString()), "modules": outputArray(outputString())}}, "title": boundedADRString("Task title.", 3, 128), "summary": boundedADRString("Task summary.", 1, 256), "objective": boundedADRString("Task objective.", 3, 200000), "acceptance_criteria": outputArray(outputString()), "constraints": outputArray(outputString()), "priority": boundedADRString("Task priority.", 0, 32), "dependencies": outputArray(outputString()), "preparation_references": outputArray(outputString()), "metadata": map[string]any{"type": "object", "additionalProperties": outputString()}, "adr_relation": outputString(), "adr_references": outputArray(outputString())}
}

func taskLifecycleCreateSchema() map[string]any {
	return obj(taskLifecycleInputProperties(), "title", "summary", "objective", "adr_relation")
}
func taskLifecycleReadSchema() map[string]any {
	return obj(map[string]any{"key": outputString(), "revision": outputInteger()}, "key")
}
func taskLifecycleUpdateSchema() map[string]any {
	p := taskLifecycleInputProperties()
	p["key"] = outputString()
	p["reason"] = boundedADRString("Mutation reason.", 1, 1024)
	return obj(p, "key", "reason")
}
func taskLifecycleListSchema() map[string]any {
	return obj(map[string]any{"cursor": outputString(), "include_archived": outputBoolean()})
}
func taskLifecycleQuerySchema() map[string]any {
	return obj(map[string]any{"cursor": outputString(), "text": outputString(), "status": outputEnum(model.TaskAuthoringPlanned, model.TaskAuthoringReady, model.TaskAuthoringDone, model.TaskAuthoringArchived), "type": taskTypeSchema()})
}
func taskLifecycleArchiveSchema() map[string]any {
	return obj(map[string]any{"key": outputString(), "reason": boundedADRString("Archive reason.", 1, 1024)}, "key", "reason")
}
func taskLifecycleHistorySchema() map[string]any {
	return obj(map[string]any{"key": outputString(), "cursor": outputString()}, "key")
}

func taskLifecycleReadOutputSchema() map[string]any {
	p := taskLifecycleInputProperties()
	properties := map[string]any{"key": outputString(), "revision": outputInteger(), "title": p["title"], "summary": p["summary"], "status": outputEnum(model.TaskAuthoringPlanned, model.TaskAuthoringReady, model.TaskAuthoringDone, model.TaskAuthoringArchived), "type": p["type"], "scope": p["scope"], "objective": p["objective"], "acceptance_criteria": p["acceptance_criteria"], "constraints": p["constraints"], "priority": p["priority"], "dependencies": p["dependencies"], "preparation_references": p["preparation_references"], "metadata": p["metadata"], "adr_relation": p["adr_relation"], "adr_references": p["adr_references"], "created_at": outputDateTime(), "updated_at": outputDateTime()}
	return closedOutput(properties, "key", "revision", "title", "status", "type", "objective", "acceptance_criteria", "constraints", "dependencies", "preparation_references", "adr_relation", "adr_references", "created_at")
}
func taskLifecycleSummarySchema() map[string]any {
	return closedOutput(map[string]any{"key": outputString(), "title": outputString(), "summary": outputString(), "status": outputEnum(model.TaskAuthoringPlanned, model.TaskAuthoringReady, model.TaskAuthoringDone, model.TaskAuthoringArchived), "revision": outputInteger(), "updated_at": outputDateTime()}, "key", "title", "summary", "status", "revision")
}
func taskLifecycleListOutputSchema() map[string]any {
	return closedOutput(map[string]any{"items": outputArray(taskLifecycleSummarySchema()), "next_cursor": outputString()}, "items")
}
func taskLifecycleHistoryOutputSchema() map[string]any {
	row := closedOutput(map[string]any{"revision": outputInteger(), "mutation_kind": outputString(), "actor": outputString(), "reason": outputString(), "changed_fields": outputArray(outputString()), "recorded_at": outputDateTime()}, "revision", "mutation_kind", "actor", "reason", "recorded_at")
	return closedOutput(map[string]any{"key": outputString(), "revisions": outputArray(row), "next_cursor": outputString()}, "key", "revisions")
}
