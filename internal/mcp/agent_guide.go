package mcp

func boundedGuideText(description string) map[string]any {
	value := outputString()
	value["maxLength"] = 768
	value["description"] = description
	return value
}

func canonicalAgentGuideOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"architecture":     boundedGuideText("The canonical supervision architecture."),
		"tail":             boundedGuideText("The canonical agent/tail selector and resolution contract."),
		"status_await":     boundedGuideText("The canonical agent/status and agent/await selector contract."),
		"prompt_interrupt": boundedGuideText("The canonical agent/prompt and agent/interrupt contract."),
		"authority":        boundedGuideText("The authority and excluded execution-state contract."),
	}, "architecture", "tail", "status_await", "prompt_interrupt", "authority")
}

func canonicalAgentGuide() map[string]any {
	return map[string]any{
		"architecture":     "Use exactly one durable Planner session and one attached enabled coding Agent; Train and watcher state are not Agent supervision authority.",
		"tail":             "agent/tail accepts {session?,lines?}. Omitted session selects the unique active durable Agent session; zero is an error and more than one requires explicit SA-*. Examples: {} or {lines:30}; explicit: {session:\"SA-GTW-BEYB\",lines:30}. The selected durable session resolves internally to its stored Airelay ref.",
		"status_await":     "agent/status and agent/await use the logical Agent key (for example gpt-review-planner); they resolve the current coding Agent internally. They do not accept an SA-* transcript selector.",
		"prompt_interrupt": "agent/prompt sends a bounded message to the resolved logical Agent. agent/interrupt cancels its current turn and may submit a bounded replacement message. Both use logical Agent identity and server authority.",
		"authority":        "Gateway schemas and handlers are the operational contract. No repo guide file, Train record, watcher, project Airelay fallback, or duplicate policy source is authoritative.",
	}
}
