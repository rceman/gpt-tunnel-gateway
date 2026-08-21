package mcp

import "github.com/rceman/gpt-tunnel-gateway/internal/airelay"

func boundedAgentMessageSchema() map[string]any {
	message := str("Bounded non-interrupting Agent prompt.")
	message["minLength"], message["maxLength"] = 1, airelay.MaxPromptBytes
	return message
}
func agentInterruptOutputSchema() map[string]any {
	return obj(map[string]any{
		"operation_id": str("Durable interrupt operation identity."), "project_id": str("Project identity."), "train_id": str("Train identity."),
		"item_position": integer("TrainItem position.", 0, 1000000), "task_id": str("Task identity."), "attempt_number": integer("Attempt number.", 1, 1000000),
		"agent_id": str("Agent identity."), "outcome": outputEnum("interrupt_acknowledged", "already_idle", "timed_out", "failed", "turn_changed", "unsupported", "stale_execution", "in_flight"),
		"interrupt_outcome": outputString(), "prompt_outcome": outputString(), "requested": map[string]any{"type": "boolean"}, "prompt_delivered": map[string]any{"type": "boolean"},
		"elapsed_ms": outputInteger(), "error": outputString(), "reason": outputString(), "started_at": outputDateTime(), "finished_at": outputDateTime(),
	}, "operation_id", "project_id", "train_id", "item_position", "task_id", "attempt_number", "agent_id", "outcome", "requested", "started_at", "finished_at")
}
