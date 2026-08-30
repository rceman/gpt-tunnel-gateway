package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) agent_action_set4() error {
	register := func(action GenericAction) error { return s.RegisterGenericAction(action) }
	if err := register(GenericAction{
		Path:          "agent/list",
		Description:   "List the project-bound Agents in deterministic key order.",
		InputSchema:   obj(map[string]any{}),
		OutputSchema:  canonicalAgentListOutputSchema(),
		Annotations:   ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
		AuthorityRole: actionRolePlannerOrDelivery,
		LocalReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct{}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			projectID, err := s.boundAgentProject(ctx)
			if err != nil {
				return nil, err
			}
			agents, err := s.Service.AgentList(ctx, projectID)
			if err != nil {
				return nil, err
			}
			result := make([]map[string]any, 0, len(agents))
			for _, agent := range agents {
				result = append(result, map[string]any{"key": agent.AgentID, "role": agent.Role, "enabled": agent.Enabled})
			}
			return map[string]any{"agents": result}, nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                "agent/status",
		Description:         "Read the compact current status of one server-selected Agent.",
		InputSchema:         canonicalAgentStatusInputSchema(),
		OutputSchema:        canonicalAgentStatusOutputSchema(),
		Annotations:         ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
		AuthorityRole:       actionRolePlannerOrDelivery,
		LocalReadOnly:       true,
		AllowLegacyOverride: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			return s.canonicalAgentStatusAction(ctx, raw)
		},
	}); err != nil {
		return err
	}
	return register(GenericAction{
		Path:          "agent/await",
		Description:   "Wait for a bounded Agent supervision transition.",
		InputSchema:   canonicalAgentAwaitInputSchema(),
		OutputSchema:  canonicalAgentAwaitOutputSchema(),
		Annotations:   ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
		AuthorityRole: actionRolePlannerOrDelivery,
		LocalReadOnly: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			return s.canonicalAgentAwaitAction(ctx, raw)
		},
	})
}

// agentStatusAction keeps the durable registry projection at the top level
// for existing callers, while adding the runtime snapshot needed by bounded
// supervision heartbeats. The tail is read through AgentTailPage so its
// observation state is scoped by the durable Gateway session, project, and
// resolved Airelay session.
func (s *Server) agentStatusAction(ctx context.Context, raw json.RawMessage) (any, error) {
	return s.agentStatusActionWithTail(ctx, raw, agentStatusTailLines, false)
}

func (s *Server) agentStatusActionWithTail(ctx context.Context, raw json.RawMessage, tailLines int, preserveBacklog bool) (any, error) {
	var in struct {
		ProjectID string `json:"project_id"`
		AgentID   string `json:"agent_id"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if in.ProjectID == "" {
		sessionID := service.AgentSessionID(ctx)
		if sessionID == "" {
			return nil, fmt.Errorf("agent/status requires a bound project")
		}
		record, err := s.activeSession(sessionID)
		if err != nil {
			return nil, err
		}
		in.ProjectID = record.ProjectID
	}
	if in.AgentID == "" {
		resolved, err := s.Service.ResolveAgent(ctx, service.AgentResolveInput{
			ProjectID:     in.ProjectID,
			Role:          model.AgentRoleCoding,
			RequireUsable: false,
		})
		if err != nil {
			return nil, fmt.Errorf("agent/status could not resolve a coding Agent: %w", err)
		}
		in.AgentID = resolved.AgentID
	}
	return s.agentStatusProjectionWithTail(ctx, in.ProjectID, in.AgentID, tailLines, preserveBacklog)
}

func (s *Server) agentStatusProjection(ctx context.Context, projectID, agentID string) (map[string]any, error) {
	return s.agentStatusProjectionWithTail(ctx, projectID, agentID, agentStatusTailLines, false)
}

func (s *Server) agentStatusProjectionWithTail(ctx context.Context, projectID, agentID string, tailLines int, preserveBacklog bool) (map[string]any, error) {
	status, err := s.Service.AgentRegistryStatus(ctx, projectID, agentID)
	if err != nil {
		return nil, err
	}
	tail, err := s.Service.AgentTailPage(ctx, projectID, service.AgentTailInput{
		Lines:           tailLines,
		SessionID:       service.AgentSessionID(ctx),
		PreserveBacklog: preserveBacklog,
	})
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		return nil, err
	}
	projection := map[string]any{}
	if err := json.Unmarshal(encoded, &projection); err != nil {
		return nil, err
	}
	projection["runtime_state"] = status.SessionState
	projection["tail"] = tail.Lines
	projection["tail_count"] = tail.Count
	projection["tail_has_new_info"] = tail.HasNewInfo
	projection["tail_history_truncated"] = tail.HistoryTruncated
	projection["tail_overflow"] = tail.Overflow
	return projection, nil
}

// agentStatusContinuationProjection deliberately omits durable identity and
// unchanged registry metadata. The tail cursor is advanced by
// AgentTailPage, so only newly observed lines are retained here.
func (s *Server) agentStatusContinuationProjection(ctx context.Context, raw json.RawMessage, requested ...int) (map[string]any, error) {
	tailLines, preserveBacklog := agentStatusTailLines, false
	if len(requested) > 0 {
		tailLines, preserveBacklog = requested[0], true
	}
	value, err := s.agentStatusActionWithTail(ctx, raw, tailLines, preserveBacklog)
	if err != nil {
		return nil, err
	}
	full, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("agent/status returned an invalid projection")
	}
	return sparseAgentStatusProjection(full), nil
}

func sparseAgentStatusProjection(full map[string]any) map[string]any {
	sparse := map[string]any{}
	if value, ok := full["runtime_state"].(string); ok && value != "" {
		sparse["runtime_state"] = value
	}
	if value, ok := full["controller_reachable"].(bool); ok && !value {
		sparse["controller_reachable"] = false
	}
	if value, ok := full["state"].(string); ok && value != "" && value != "usable" && value != "registered" {
		sparse["state"] = value
		if reason, ok := full["reason"].(string); ok && reason != "" {
			sparse["reason"] = reason
		}
	}
	if value, ok := full["error"].(string); ok && value != "" {
		sparse["error"] = value
	}
	for _, field := range []string{"attempt_state", "train_id", "task_id", "attempt_number", "item_position", "recoverable", "recovery_reason"} {
		if value, ok := full[field]; ok {
			sparse[field] = value
		}
	}
	if tail, ok := full["tail"].([]string); ok && len(tail) > 0 {
		sparse["tail"] = tail
	}
	if truncated, ok := full["tail_history_truncated"].(bool); ok && truncated {
		sparse["tail_history_truncated"] = true
	}
	if overflow, ok := full["tail_overflow"].(bool); ok && overflow {
		sparse["tail_overflow"] = true
	}
	return sparse
}
