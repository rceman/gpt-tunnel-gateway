package mcp

import "github.com/rceman/gpt-tunnel-gateway/internal/airelay"

const canonicalAgentMessageMaxBytes = 240

func boundedAgentMessageSchema() map[string]any {
	message := str("Bounded non-interrupting Agent prompt.")
	message["minLength"], message["maxLength"] = 1, airelay.MaxPromptBytes
	return message
}

func canonicalAgentSelectorSchema() map[string]any {
	agent := str("Optional server-resolved Agent key.")
	agent["maxLength"] = 128
	return agent
}

func canonicalAgentMessageSchema() map[string]any {
	message := str("Bounded Agent message.")
	message["minLength"], message["maxLength"] = 1, canonicalAgentMessageMaxBytes
	return message
}

func canonicalAgentStatusInputSchema() map[string]any {
	return obj(map[string]any{"agent": canonicalAgentSelectorSchema()})
}

func canonicalAgentListOutputSchema() map[string]any {
	item := closedOutput(map[string]any{
		"key":     outputString(),
		"role":    outputString(),
		"enabled": outputBoolean(),
	}, "key", "role", "enabled")
	return closedOutput(map[string]any{"agents": outputArray(item)}, "agents")
}

func canonicalAgentStatusOutputSchema() map[string]any {
	properties := map[string]any{
		"agent":  outputString(),
		"status": outputEnum("idle", "busy", "unavailable", "disabled"),
		"task":   outputString(),
		"train":  outputString(),
		"hotfix": outputString(),
	}
	return closedOutput(properties, "agent", "status")
}

func canonicalAgentAwaitInputSchema() map[string]any {
	seconds := integer("Maximum seconds to await a meaningful Agent supervision transition.", 1, 600)
	seconds["default"] = 60
	return obj(map[string]any{"agent": canonicalAgentSelectorSchema(), "seconds": seconds})
}

func canonicalAgentAwaitOutputSchema() map[string]any {
	properties := map[string]any{
		"agent":          outputString(),
		"status":         outputEnum("idle", "busy", "unavailable", "disabled"),
		"task":           outputString(),
		"train":          outputString(),
		"hotfix":         outputString(),
		"tail":           outputArray(outputString()),
		"tail_truncated": outputBoolean(),
	}
	return closedOutput(properties, "agent", "status")
}

func canonicalAgentTailInputSchema() map[string]any {
	lines := integer("Maximum transcript lines to return.", 1, 200)
	lines["default"] = 30
	return obj(map[string]any{"agent": canonicalAgentSelectorSchema(), "lines": lines})
}

func canonicalAgentTailOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"agent":     outputString(),
		"lines":     outputArray(outputString()),
		"truncated": outputBoolean(),
	}, "agent", "lines")
}

func canonicalAgentPromptInputSchema() map[string]any {
	return obj(map[string]any{"agent": canonicalAgentSelectorSchema(), "message": canonicalAgentMessageSchema()}, "message")
}

func canonicalAgentInterruptInputSchema() map[string]any {
	return obj(map[string]any{"agent": canonicalAgentSelectorSchema(), "message": canonicalAgentMessageSchema()})
}

func canonicalAgentOperationOutputSchema() map[string]any {
	return closedOutput(map[string]any{"operation": outputString(), "status": outputString()}, "operation", "status")
}
