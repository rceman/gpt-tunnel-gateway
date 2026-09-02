package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

type canonicalAgentTarget struct {
	Agent    model.Agent
	Resolved service.ResolvedAgent
}

const (
	canonicalAgentAwaitDefaultSeconds  = 50
	canonicalAgentAwaitFinalReadBudget = time.Second
)

func (s *Server) boundAgentProject(ctx context.Context) (string, error) {
	sessionID := service.AgentSessionID(ctx)
	if sessionID == "" {
		return "", fmt.Errorf("Agent action requires a bound session")
	}
	record, err := s.activeSession(sessionID)
	if err != nil {
		return "", err
	}
	if record.ProjectID == "" {
		return "", fmt.Errorf("Agent action requires a bound project")
	}
	return record.ProjectID, nil
}

func (s *Server) resolveCanonicalAgent(ctx context.Context, projectID, requested string, requireEnabled bool) (canonicalAgentTarget, error) {
	if requested != "" {
		if model.ValidateObjectIdentifier(requested) != nil {
			return canonicalAgentTarget{}, fmt.Errorf("invalid Agent selector")
		}
	}
	if requested != "" {
		selected, err := s.Service.AgentRead(ctx, projectID, requested)
		if err != nil {
			return canonicalAgentTarget{}, err
		}
		if requireEnabled && !selected.Enabled {
			return canonicalAgentTarget{}, fmt.Errorf("Agent %q is disabled", selected.AgentID)
		}
		target := canonicalAgentTarget{Agent: selected}
		if requireEnabled {
			target.Resolved, err = s.Service.ResolveAgent(ctx, service.AgentResolveInput{
				ProjectID: projectID,
				Role:      selected.Role,
				AgentID:   selected.AgentID,
			})
			if err != nil {
				return canonicalAgentTarget{}, err
			}
		}
		return target, nil
	}
	agents, err := s.Service.AgentList(ctx, projectID)
	if err != nil {
		return canonicalAgentTarget{}, err
	}
	var selected *model.Agent
	candidates := make([]model.Agent, 0, len(agents))
	for _, agent := range agents {
		if agent.Role == model.AgentRoleCoding && agent.Enabled {
			candidates = append(candidates, agent)
		}
	}
	if len(candidates) == 0 {
		return canonicalAgentTarget{}, fmt.Errorf("AGENT_NOT_AVAILABLE: no enabled coding Agent for project %q", projectID)
	}
	if len(candidates) > 1 {
		active := make([]model.Agent, 0)
		for _, agent := range candidates {
			status, statusErr := s.Service.AgentRegistryStatus(ctx, projectID, agent.AgentID)
			if statusErr != nil {
				return canonicalAgentTarget{}, fmt.Errorf("Agent selection is unavailable: %w", statusErr)
			}
			if status.SessionState == "running" || status.SessionState == "waiting" {
				active = append(active, agent)
			}
		}
		if len(active) == 1 {
			selected = &active[0]
		} else {
			return canonicalAgentTarget{}, fmt.Errorf("AGENT_SELECTION_REQUIRED: multiple enabled coding Agents are applicable")
		}
	} else {
		selected = &candidates[0]
	}
	if requireEnabled && !selected.Enabled {
		return canonicalAgentTarget{}, fmt.Errorf("Agent %q is disabled", selected.AgentID)
	}
	target := canonicalAgentTarget{Agent: *selected}
	if requireEnabled {
		target.Resolved, err = s.Service.ResolveAgent(ctx, service.AgentResolveInput{
			ProjectID: projectID,
			Role:      selected.Role,
			AgentID:   selected.AgentID,
		})
		if err != nil {
			return canonicalAgentTarget{}, err
		}
	}
	return target, nil
}

func (s *Server) resolveCanonicalInterruptAgent(ctx context.Context, projectID, requested string) (canonicalAgentTarget, error) {
	if requested != "" {
		if model.ValidateObjectIdentifier(requested) != nil {
			return canonicalAgentTarget{}, fmt.Errorf("invalid Agent selector")
		}
		agent, err := s.Service.AgentRead(ctx, projectID, requested)
		if err != nil {
			return canonicalAgentTarget{}, err
		}
		if !agent.Enabled {
			return canonicalAgentTarget{}, fmt.Errorf("Agent %q is disabled", agent.AgentID)
		}
		resolved, err := s.Service.ResolveAgent(ctx, service.AgentResolveInput{
			ProjectID: projectID,
			Role:      agent.Role,
			AgentID:   agent.AgentID,
		})
		if err != nil {
			return canonicalAgentTarget{}, err
		}
		return canonicalAgentTarget{Agent: agent, Resolved: resolved}, nil
	}
	resolved, err := s.Service.ResolveAgent(ctx, service.AgentResolveInput{ProjectID: projectID, Role: model.AgentRoleCoding})
	if err != nil {
		return canonicalAgentTarget{}, err
	}
	agent, err := s.Service.AgentRead(ctx, projectID, resolved.AgentID)
	if err != nil {
		return canonicalAgentTarget{}, err
	}
	if !agent.Enabled {
		return canonicalAgentTarget{}, fmt.Errorf("Agent %q is disabled", agent.AgentID)
	}
	return canonicalAgentTarget{Agent: agent, Resolved: resolved}, nil
}

func validateCanonicalAgentMessage(message string) error {
	if !utf8.ValidString(message) || len(message) < 1 || len([]byte(message)) > canonicalAgentMessageMaxBytes || strings.ContainsRune(message, 0) {
		return fmt.Errorf("Agent message must be valid UTF-8, 1-%d bytes, and contain no NUL", canonicalAgentMessageMaxBytes)
	}
	return nil
}

func validateOptionalCanonicalAgentMessage(message string) error {
	if message == "" {
		return nil
	}
	return validateCanonicalAgentMessage(message)
}

func canonicalAgentStatus(ctx context.Context, s *Server, projectID string, target canonicalAgentTarget) (map[string]any, error) {
	availability, err := s.Service.AgentRegistryStatus(ctx, projectID, target.Agent.AgentID)
	if err != nil {
		return nil, err
	}
	state := canonicalAgentAvailabilityState(availability)
	result := map[string]any{"agent": availability.AgentID, "status": state}
	if availability.TaskID != "" {
		result["task"] = availability.TaskID
	}
	if availability.TrainID != "" {
		result["train"] = availability.TrainID
	}
	return result, nil
}

func canonicalAgentAvailabilityState(availability model.AgentAvailabilityStatus) string {
	switch {
	case !availability.Enabled || availability.State == "disabled":
		return "disabled"
	case availability.SessionState == "running" || availability.SessionState == "waiting":
		return "busy"
	case availability.State == "unavailable" || availability.State == "unbound":
		return "unavailable"
	default:
		return "idle"
	}
}

func (s *Server) canonicalAgentStatusAction(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		Agent string `json:"agent"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	projectID, err := s.boundAgentProject(ctx)
	if err != nil {
		return nil, err
	}
	target, err := s.resolveCanonicalAgent(ctx, projectID, in.Agent, false)
	if err != nil {
		return nil, err
	}
	return canonicalAgentStatus(ctx, s, projectID, target)
}

func (s *Server) canonicalAgentTailAction(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		Agent string `json:"agent"`
		Lines int    `json:"lines"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	projectID, err := s.boundAgentProject(ctx)
	if err != nil {
		return nil, err
	}
	target, err := s.resolveCanonicalAgent(ctx, projectID, in.Agent, true)
	if err != nil {
		return nil, err
	}
	tail, err := s.Service.AgentTailPage(ctx, projectID, service.AgentTailInput{
		Lines:      in.Lines,
		SessionID:  service.AgentSessionID(ctx),
		SessionKey: target.Resolved.SessionKey,
	})
	if err != nil {
		return nil, err
	}
	result := map[string]any{"agent": target.Agent.AgentID, "lines": tail.Lines}
	if tail.HistoryTruncated || tail.Overflow {
		result["truncated"] = true
	}
	return result, nil
}

func newCanonicalAgentOperationID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create Agent operation identity: %w", err)
	}
	return "mutation-agent-" + hex.EncodeToString(value[:]), nil
}

func (s *Server) canonicalAgentAwaitAction(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		Agent   string `json:"agent"`
		Seconds *int   `json:"seconds"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	seconds := canonicalAgentAwaitDefaultSeconds
	if in.Seconds != nil {
		seconds = *in.Seconds
	}
	if seconds < 1 || seconds > 600 {
		return nil, fmt.Errorf("seconds must be between 1 and 600")
	}
	awaitDuration := time.Duration(seconds) * time.Second
	awaitCtx, cancel := context.WithTimeout(ctx, awaitDuration)
	defer cancel()
	finalReadBudget := canonicalAgentAwaitFinalReadBudget
	if half := awaitDuration / 2; half < finalReadBudget {
		finalReadBudget = half
	}
	finalReadTimer := time.NewTimer(awaitDuration - finalReadBudget)
	defer finalReadTimer.Stop()
	projectID, err := s.boundAgentProject(awaitCtx)
	if err != nil {
		return nil, err
	}
	target, err := s.resolveCanonicalAgent(awaitCtx, projectID, in.Agent, true)
	if err != nil {
		return nil, err
	}
	if _, err := s.Service.AgentTailPage(awaitCtx, projectID, service.AgentTailInput{
		Lines: 30, SessionID: service.AgentSessionID(ctx), SessionKey: target.Resolved.SessionKey,
	}); err != nil {
		return nil, err
	}
	previous, err := canonicalAgentStatus(awaitCtx, s, projectID, target)
	if err != nil {
		return nil, err
	}
	previousDigest, err := json.Marshal(previous)
	if err != nil {
		return nil, err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-awaitCtx.Done():
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return previous, nil
		case <-finalReadTimer.C:
			finalCtx, finalCancel := context.WithTimeout(awaitCtx, finalReadBudget)
			current, statusErr := canonicalAgentStatus(finalCtx, s, projectID, target)
			if statusErr != nil {
				finalCancel()
				return nil, statusErr
			}
			result, resultErr := s.canonicalAgentAwaitResult(finalCtx, projectID, target, current)
			finalCancel()
			return result, resultErr
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			current, statusErr := canonicalAgentStatus(awaitCtx, s, projectID, target)
			if statusErr != nil {
				return nil, statusErr
			}
			currentDigest, marshalErr := json.Marshal(current)
			if marshalErr != nil {
				return nil, marshalErr
			}
			if string(currentDigest) == string(previousDigest) {
				continue
			}
			return s.canonicalAgentAwaitResult(awaitCtx, projectID, target, current)
		}
	}
}

func (s *Server) canonicalAgentAwaitResult(ctx context.Context, projectID string, target canonicalAgentTarget, current map[string]any) (map[string]any, error) {
	tail, err := s.Service.AgentTailPage(ctx, projectID, service.AgentTailInput{
		Lines: 30, SessionID: service.AgentSessionID(ctx), SessionKey: target.Resolved.SessionKey,
	})
	if err != nil {
		return nil, err
	}
	if len(tail.Lines) > 0 {
		current["tail"] = tail.Lines
	}
	if tail.HistoryTruncated || tail.Overflow {
		current["tail_truncated"] = true
	}
	return current, nil
}
