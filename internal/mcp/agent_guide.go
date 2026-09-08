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
		"architecture":     "The Planner role may have multiple durable Planner sessions; the project has exactly one attached enabled coding Agent. Train and watcher state are not Agent supervision authority.",
		"tail":             "agent/tail accepts {session?,lines?}. Omitted session selects the unique active durable Agent session; zero is an error and more than one requires explicit SA-*. Examples: {} or {lines:30}; explicit: {session:\"SA-GTW-AB12\",lines:30}. The selected durable session resolves internally to its stored Airelay ref.",
		"status_await":     "agent/status and agent/await accept an optional logical Agent selector; when omitted, they use the server-selected attached coding Agent. They do not accept a durable SA-* selector.",
		"prompt_interrupt": "agent/prompt and agent/interrupt accept an optional logical Agent selector; when omitted, they use the server-selected attached coding Agent. Prompt sends a bounded message; interrupt cancels the current turn and may submit a bounded replacement. Neither accepts a durable SA-* selector.",
		"authority":        "Gateway schemas and handlers are the operational contract. No repo guide file, Train record, watcher, project Airelay fallback, or duplicate policy source is authoritative.",
	}
}
