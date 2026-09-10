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
		"role_authority":     boundedGuideText("The Planner and Agent authority boundary."),
		"startup":            boundedGuideText("The exact assigned-worktree startup preflight."),
		"canonical_state":    boundedGuideText("Supported state authorities and prohibited scans."),
		"exploration_budget": boundedGuideText("The bounded repository exploration budget."),
		"stop_fast":          boundedGuideText("Fail-closed stop conditions."),
		"checkpoints":        boundedGuideText("Production and tests-only checkpoint discipline."),
		"testing":            boundedGuideText("ADR118 rev2 deterministic testing guidance."),
		"execution_example":  boundedGuideText("A concise good example and explicit wasteful bad example."),
		"cli_usage":          boundedGuideText("Supported zero-state and safe read-only CLI examples."),
		"architecture":       boundedGuideText("The canonical supervision architecture."),
		"tail":               boundedGuideText("The canonical agent/tail selector and resolution contract."),
		"status_await":       boundedGuideText("The canonical agent/status and agent/await selector contract."),
		"prompt_interrupt":   boundedGuideText("The canonical agent/prompt and agent/interrupt contract."),
		"authority":          boundedGuideText("The authority and excluded execution-state contract."),
	}, "role_authority", "startup", "canonical_state", "exploration_budget", "stop_fast", "checkpoints", "testing", "execution_example", "cli_usage", "architecture", "tail", "status_await", "prompt_interrupt", "authority")
}

func canonicalAgentGuide() map[string]any {
	content := agentguide.Canonical()
	return map[string]any{
		"role_authority":     content.RoleAuthority,
		"startup":            content.Startup,
		"canonical_state":    content.CanonicalState,
		"exploration_budget": content.Exploration,
		"stop_fast":          content.StopFast,
		"checkpoints":        content.Checkpoints,
		"testing":            content.Testing,
		"execution_example":  content.ExecutionExample,
		"cli_usage":          content.CLIUsage,
		"architecture":       content.Architecture,
		"tail":               content.Tail,
		"status_await":       content.StatusAwait,
		"prompt_interrupt":   content.PromptInterrupt,
		"authority":          content.Authority,
	}
}
