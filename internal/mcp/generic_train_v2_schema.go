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

func trainV2RetireSchema() map[string]any {
	return obj(map[string]any{
		"train_id":              str("Train identifier within the bound session project."),
		"reason":                str("Bounded server-recorded retirement reason."),
		"expected_hub_revision": str("Optimistic Hub revision."),
	}, "train_id", "reason")
}

func trainV2ReconcileSchema() map[string]any {
	return obj(map[string]any{
		"apply":                 map[string]any{"type": "boolean", "description": "Apply only exact safe stale classifications; false is a dry-run."},
		"reason":                str("Optional bounded reconciliation reason."),
		"expected_hub_revision": str("Optimistic Hub revision required for apply."),
	}, "apply")
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

func trainV2ReviewBackfillSchema() map[string]any {
	return obj(map[string]any{
		"project_id":            str("Registered project identifier."),
		"train_id":              str("Server-allocated Train identifier."),
		"item_start":            integer("Inclusive first item position.", 0, 1000000),
		"item_end":              integer("Inclusive last item position.", 0, 1000000),
		"apply":                 map[string]any{"type": "boolean", "description": "Apply the validated migration; false performs a dry-run."},
		"expected_hub_revision": str("Optimistic Hub revision required for apply."),
	}, "project_id", "train_id", "item_start", "item_end", "apply")
}

func trainV2AttemptFinalizeSchema() map[string]any {
	return obj(map[string]any{
		"project_id":            str("Registered project identifier."),
		"train_id":              str("Server-allocated Train identifier."),
		"item_position":         integer("Zero-based TrainItem position.", 0, 1000000),
		"attempt_number":        integer("TrainItem-local Attempt number.", 1, 1000000),
		"summary":               str("Optional server-owned summary."),
		"acceptance_coverage":   array(str("Acceptance criterion identifier.")),
		"deviations":            array(str("Bounded deviation.")),
		"remaining_risks":       array(str("Bounded remaining risk.")),
		"expected_hub_revision": str("Optimistic Hub revision."),
	}, "project_id", "train_id", "item_position", "attempt_number")
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
