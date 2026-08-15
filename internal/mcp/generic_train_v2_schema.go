package mcp

func trainV2OutputSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}

func trainV2CreateSchema() map[string]any {
	return obj(map[string]any{
		"project_id":            str("Registered project identifier."),
		"task_ids":              array(str("Ready Task identifier.")),
		"created_by":            str("Author identity."),
		"expected_hub_revision": str("Optimistic Hub revision."),
	}, "project_id", "task_ids", "created_by")
}

func trainV2AddSchema() map[string]any {
	return obj(map[string]any{
		"project_id":            str("Registered project identifier."),
		"train_id":              str("Server-allocated Train identifier."),
		"task_ids":              array(str("Ready Task identifier.")),
		"expected_revision":     integer("Exact Train revision.", 1, 1000000),
		"added_by":              str("Author identity."),
		"expected_hub_revision": str("Optimistic Hub revision."),
	}, "project_id", "train_id", "task_ids", "expected_revision", "added_by")
}

func trainV2ReadSchema() map[string]any {
	return obj(map[string]any{"project_id": str("Registered project identifier."), "train_id": str("Server-allocated Train identifier.")}, "project_id", "train_id")
}

func trainV2ListSchema() map[string]any {
	return obj(map[string]any{"project_id": str("Registered project identifier."), "limit": integer("Maximum Trains.", 1, 32)}, "project_id")
}

func trainV2StartSchema() map[string]any {
	return obj(map[string]any{
		"project_id":            str("Registered project identifier."),
		"train_id":              str("Server-allocated Train identifier."),
		"started_by":            str("Author identity."),
		"agent_id":              str("Optional coding Agent identity."),
		"recommended_reasoning": str("Optional reasoning preference."),
		"expected_hub_revision": str("Optimistic Hub revision."),
	}, "project_id", "train_id", "started_by")
}

func trainV2AdvanceSchema() map[string]any {
	return obj(map[string]any{
		"project_id":            str("Registered project identifier."),
		"train_id":              str("Server-allocated Train identifier."),
		"expected_hub_revision": str("Optimistic Hub revision."),
	}, "project_id", "train_id")
}

func trainV2IntegrateSchema() map[string]any {
	return obj(map[string]any{
		"project_id":            str("Registered project identifier."),
		"train_id":              str("Server-allocated Train identifier."),
		"expected_hub_revision": str("Optimistic Hub revision."),
	}, "project_id", "train_id")
}

func trainV2FullProofSchema() map[string]any {
	return obj(map[string]any{
		"project_id":            str("Registered project identifier."),
		"train_id":              str("Server-allocated Train identifier."),
		"expected_hub_revision": str("Optimistic Hub revision."),
	}, "project_id", "train_id")
}

func trainV2AttemptFinalizeSchema() map[string]any {
	return obj(map[string]any{
		"project_id":            str("Registered project identifier."),
		"train_id":              str("Server-allocated Train identifier."),
		"item_position":         integer("Zero-based TrainItem position.", 0, 1000000),
		"attempt_number":        integer("TrainItem-local Attempt number.", 1, 1000000),
		"completion_file":       str("Canonical local Attempt completion receipt path."),
		"summary":               str("Optional server-owned summary."),
		"expected_hub_revision": str("Optimistic Hub revision."),
	}, "project_id", "train_id", "item_position", "attempt_number", "completion_file")
}

func trainV2AttemptReviewSchema() map[string]any {
	return obj(map[string]any{
		"project_id":            str("Registered project identifier."),
		"train_id":              str("Server-allocated Train identifier."),
		"item_position":         integer("Zero-based TrainItem position.", 0, 1000000),
		"attempt_number":        integer("TrainItem-local Attempt number.", 1, 1000000),
		"outcome":               str("Planner review outcome."),
		"reviewed_head":         str("Reviewed immutable repository head."),
		"findings":              array(map[string]any{"type": "object", "additionalProperties": true}),
		"scope_coverage":        array(map[string]any{"type": "object", "additionalProperties": true}),
		"expected_hub_revision": str("Optimistic Hub revision."),
	}, "project_id", "train_id", "item_position", "attempt_number", "outcome", "reviewed_head")
}

func trainV2AttemptProofRecoverySchema() map[string]any {
	return obj(map[string]any{
		"project_id":            str("Registered project identifier."),
		"train_id":              str("Server-allocated Train identifier."),
		"item_position":         integer("Zero-based TrainItem position.", 0, 1000000),
		"attempt_number":        integer("TrainItem-local Attempt number.", 1, 1000000),
		"expected_hub_revision": str("Optimistic Hub revision."),
	}, "project_id", "train_id", "item_position", "attempt_number")
}

func trainV2CutoverSchema() map[string]any {
	return obj(map[string]any{
		"project_id":                   str("Registered project identifier."),
		"materialization_acknowledged": map[string]any{"type": "boolean", "description": "Explicit acknowledgement that relevant roadmap work was materialized or archived."},
		"plan_retirement_acknowledged": map[string]any{"type": "boolean", "description": "Explicit acknowledgement that Plan becomes historical/read-only."},
		"updated_by":                   str("Authority identity."),
		"expected_hub_revision":        str("Optimistic Hub revision."),
	}, "project_id", "materialization_acknowledged", "plan_retirement_acknowledged", "updated_by")
}
