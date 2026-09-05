package mcp

func (s *Server) ensureAgentActions() {
	if s.Service == nil {
		return
	}
	s.agentActions.Do(func() {
		s.agentActionErr = s.registerAgentActions()
	})
	if s.agentActionErr != nil {
		panic(s.agentActionErr)
	}
}
func agentInputSchema() map[string]any {
	return obj(map[string]any{
		"schema_version":        integer("Agent schema version.", 1, 1),
		"project_id":            str("Registered project identifier."),
		"agent_id":              str("Stable project-scoped agent identifier."),
		"role":                  str("Agent role: coding."),
		"enabled":               map[string]any{"type": "boolean"},
		"recommended_reasoning": str("Routing preference: low, medium, high, max, or best_available."),
		"capabilities":          array(str("Bounded capability identifier.")),
		"created_at":            str("Agent creation timestamp."),
		"updated_at":            str("Agent update timestamp."),
	}, "schema_version", "project_id", "agent_id", "role", "enabled", "recommended_reasoning", "capabilities", "created_at", "updated_at")
}
func agentObjectOutputSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}
func agentPromptOutputSchema() map[string]any {
	return map[string]any{
		"oneOf": []any{
			closedOutput(map[string]any{
				"project_id": outputString(),
				"delivered":  map[string]any{"type": "boolean", "const": true},
			}, "project_id", "delivered"),
			closedOutput(map[string]any{
				"project_id": outputString(),
				"delivered":  map[string]any{"type": "boolean", "const": false},
				"exit_code":  outputInteger(),
				"stderr":     outputString(),
				"error":      outputString(),
			}, "project_id", "delivered"),
		},
	}
}
func (s *Server) registerAgentActions() error {
	if err := s.agent_action_set1(); err != nil {
		return err
	}
	if err := s.agent_action_set2(); err != nil {
		return err
	}
	if err := s.agent_action_set3(); err != nil {
		return err
	}
	if err := s.agent_action_set4(); err != nil {
		return err
	}
	return nil
}
