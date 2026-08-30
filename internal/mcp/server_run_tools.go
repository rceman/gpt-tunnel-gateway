package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
)

// addRunTools retains only non-Run Agent supervision tools. Train-v2
// execution is addressed by Train/item/Attempt actions; the run/* application
// surface is intentionally not registered after the hard cutover.
func (s *Server) addRunTools(add toolAdder) {
	message := str("Bounded message to the registered project session")
	message["minLength"], message["maxLength"] = 1, airelay.MaxPromptBytes
	add("agent_send", "Send one bounded message to the configured project Airelay session.", obj(map[string]any{"project_id": str("Registered project identifier"), "message": message}, "project_id", "message"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		projectID, err := getString(raw, "project_id")
		if err != nil {
			return nil, err
		}
		text, err := getString(raw, "message")
		if err != nil {
			return nil, err
		}
		return s.Service.AgentSend(ctx, projectID, text)
	})
	add("agent_tail", "Read a bounded incremental transcript window from the configured project Airelay session.", agentTailInputSchema(false), func(ctx context.Context, raw json.RawMessage) (any, error) { return s.agentTailAction(ctx, raw) })
}

func agentTailInputSchema(legacySkip bool) map[string]any {
	cursor := str("Server-owned tail cursor; new values are <=8 safe characters and legacy values are accepted")
	cursor["maxLength"] = 4096
	properties := map[string]any{"project_id": str("Registered project identifier"), "lines": integer("Number of lines", 1, 200), "cursor": cursor}
	if legacySkip {
		properties["skip"] = integer("Newest lines to skip", 0, 196)
	}
	return obj(properties, "project_id")
}

func agentTailSessionInputSchema() map[string]any {
	return canonicalAgentTailInputSchema()
}

func agentTailExecutionInputSchema() map[string]any {
	lines := integer("Maximum number of transcript lines to return.", 1, 200)
	return obj(map[string]any{
		"project_id": str("Server-injected registered project identifier."),
		"agent":      canonicalAgentSelectorSchema(),
		"lines":      lines,
	}, "project_id")
}

func (s *Server) agentTailAction(ctx context.Context, raw json.RawMessage) (any, error) {
	return s.canonicalAgentTailAction(ctx, raw)
}
