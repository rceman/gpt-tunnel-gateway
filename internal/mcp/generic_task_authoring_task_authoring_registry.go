package mcp

import (
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Server) ensureTaskAuthoringActions() {
	s.taskAuthoringActions.Do(func() {
		s.taskAuthoringActionErr = s.registerTaskAuthoringActions()
	})
	if s.taskAuthoringActionErr != nil {
		panic(s.taskAuthoringActionErr)
	}
}
func taskAuthoringProperties() map[string]any {
	priority := str("Bounded authoring priority.")
	priority["maxLength"] = 32
	metadata := map[string]any{"type": "object", "additionalProperties": str("Bounded preparation metadata value.")}
	relation := str("Closed ADR relation.")
	relation["enum"] = []any{model.TaskADRNoRequired, model.TaskADRImplementsExisting, model.TaskADRRequiresNew, model.TaskADRSupersedesExisting}
	return map[string]any{
		"project_id": str("Registered project identifier."), "task_id": str("Stable Task identifier."),
		"type":  taskTypeSchema(),
		"title": str("Task title."), "objective": str("Task objective."),
		"acceptance_criteria": array(str("Acceptance criterion.")), "constraints": array(str("Task constraint.")),
		"priority": priority, "dependencies": array(str("Bounded Task dependency.")),
		"preparation_references": array(str("Preparation reference.")), "metadata": metadata,
		"adr_relation": relation, "adr_references": array(str("Accepted ADR identifier.")),
		"created_by": str("Author identity."), "updated_by": str("Author identity."), "ready_by": str("Ready seal author identity."),
		"expected_revision":        integer("Expected authoring revision.", 1, 1000000),
		"expected_revision_sha256": str("Optional exact authoring revision hash."),
		"expected_hub_revision":    str("Optimistic Hub revision."),
	}
}
func taskAuthoringCreateSchema() map[string]any {
	all := taskAuthoringProperties()
	for _, key := range []string{"task_id", "updated_by", "ready_by", "expected_revision", "expected_revision_sha256"} {
		delete(all, key)
	}
	return obj(all, "project_id", "title", "objective", "adr_relation", "created_by")
}

func taskTypeSchema() map[string]any {
	typ := str("Task classification.")
	typ["enum"] = model.TaskTypes()
	typ["default"] = string(model.TaskTypeTask)
	return typ
}
func taskAuthoringUpdateSchema() map[string]any {
	properties := taskAuthoringProperties()
	delete(properties, "created_by")
	delete(properties, "ready_by")
	return obj(properties, "project_id", "task_id", "expected_revision", "updated_by")
}
