package mcp

import "github.com/rceman/gpt-tunnel-gateway/internal/agentguide"

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
	content := agentguide.Canonical()
	return map[string]any{
		"architecture":     content.Architecture,
		"tail":             content.Tail,
		"status_await":     content.StatusAwait,
		"prompt_interrupt": content.PromptInterrupt,
		"authority":        content.Authority,
	}
}
