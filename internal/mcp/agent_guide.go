package mcp

import "github.com/rceman/gpt-tunnel-gateway/internal/agentguide"

func canonicalAgentGuide() map[string]any {
	content := agentguide.Canonical()
	return map[string]any{
		"role_authority":     content.RoleAuthority,
		"delegation":         content.Delegation,
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
