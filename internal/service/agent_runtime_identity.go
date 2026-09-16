package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

// RuntimeRoleSession is the server-owned identity chain for one request from a
// managed Airelay runtime. Runtime keys and bindings remain local authority;
// this projection is never serialized into a Task or public execution result.
type RuntimeRoleSession struct {
	ProjectID           string
	Agent               model.Agent
	Binding             config.AgentBinding
	Session             durableSession.Record
	RuntimeState        string
	ControllerReachable bool
}

type runtimeAgentCandidate struct {
	projectID string
	agent     model.Agent
	binding   config.AgentBinding
}

// ResolveRuntimeRoleSession resolves the calling Airelay runtime to exactly one
// enabled managed Agent and one active role-bound Session. It intentionally
// does not allocate, update, or end a Session and does not probe or retarget
// the Airelay process.
func (s *Service) ResolveRuntimeRoleSession(ctx context.Context, runtimeKey, role string) (RuntimeRoleSession, error) {
	if runtimeKey == "" || strings.TrimSpace(runtimeKey) != runtimeKey {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_REQUIRED: managed Airelay runtime identity is required")
	}
	if role != "" && !runtimeRoleAllowed(role) {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_ROLE_UNAUTHORIZED: unsupported role")
	}
	selected, err := s.resolveManagedAgentForRuntime(ctx, runtimeKey)
	if err != nil {
		return RuntimeRoleSession{}, err
	}
	return s.resolveRuntimeRoleSessionForAgent(selected, role)
}

func (s *Service) resolveManagedAgentForRuntime(ctx context.Context, runtimeKey string) (runtimeAgentCandidate, error) {
	if s.Durability == nil || s.Durability.Local == nil {
		return runtimeAgentCandidate{}, fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: local session authority is unavailable")
	}
	projectIDs, err := s.EffectiveProjectIDs()
	if err != nil {
		return runtimeAgentCandidate{}, fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: project registry is unavailable: %w", err)
	}
	candidates := make([]runtimeAgentCandidate, 0, len(projectIDs))
	for _, projectID := range projectIDs {
		project, projectErr := s.ProjectRead(ctx, projectID)
		if projectErr != nil {
			return runtimeAgentCandidate{}, fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: project authority is unavailable: %w", projectErr)
		}
		if project.Status != "active" {
			continue
		}
		agents, listErr := s.AgentList(ctx, projectID)
		if listErr != nil {
			return runtimeAgentCandidate{}, fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: managed Agent registry is unavailable: %w", listErr)
		}
		for _, agent := range agents {
			if !agent.Enabled || agent.Role != model.AgentRoleCoding {
				continue
			}
			binding, bound := s.agentBinding(projectID, agent.AgentID)
			if !bound || binding.Validate() != nil || binding.SessionKey != runtimeKey {
				continue
			}
			candidates = append(candidates, runtimeAgentCandidate{
				projectID: projectID,
				agent:     agent,
				binding:   binding,
			})
		}
	}
	if len(candidates) == 0 {
		return runtimeAgentCandidate{}, fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: no enabled managed Agent matches the calling runtime")
	}
	if len(candidates) != 1 {
		return runtimeAgentCandidate{}, fmt.Errorf("RUNTIME_IDENTITY_AMBIGUOUS: calling runtime matches multiple enabled managed Agents")
	}
	return candidates[0], nil
}

func (s *Service) validateManagedRuntimeBindingCollision(ctx context.Context, candidate model.Agent) error {
	if !candidate.Enabled || candidate.Role != model.AgentRoleCoding {
		return nil
	}
	binding, bound := s.agentBinding(candidate.ProjectID, candidate.AgentID)
	if !bound {
		return nil
	}
	if err := binding.Validate(); err != nil {
		return fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: invalid runtime binding for Agent %q: %w", candidate.AgentID, err)
	}
	projectIDs, err := s.EffectiveProjectIDs()
	if err != nil {
		return fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: project registry is unavailable: %w", err)
	}
	for _, projectID := range projectIDs {
		agents, listErr := s.AgentList(ctx, projectID)
		if listErr != nil {
			return fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: managed Agent registry is unavailable: %w", listErr)
		}
		if err := s.validateManagedRuntimeBindingAgainst(candidate, agents); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) validateManagedRuntimeBindingAgainst(candidate model.Agent, existing []model.Agent) error {
	if !candidate.Enabled || candidate.Role != model.AgentRoleCoding {
		return nil
	}
	binding, bound := s.agentBinding(candidate.ProjectID, candidate.AgentID)
	if !bound {
		return nil
	}
	if err := binding.Validate(); err != nil {
		return fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: invalid runtime binding for Agent %q: %w", candidate.AgentID, err)
	}
	for _, other := range existing {
		if other.ProjectID == candidate.ProjectID && other.AgentID == candidate.AgentID {
			continue
		}
		if !other.Enabled || other.Role != model.AgentRoleCoding {
			continue
		}
		otherBinding, otherBound := s.agentBinding(other.ProjectID, other.AgentID)
		if !otherBound || otherBinding.Validate() != nil || otherBinding.SessionKey != binding.SessionKey {
			continue
		}
		return fmt.Errorf("RUNTIME_IDENTITY_AMBIGUOUS: runtime binding %q is already assigned to enabled Agent %q/%q", binding.SessionKey, other.ProjectID, other.AgentID)
	}
	return nil
}

func (s *Service) ResolveRuntimeAgentSession(ctx context.Context, runtimeKey string) (RuntimeRoleSession, error) {
	if runtimeKey == "" || strings.TrimSpace(runtimeKey) != runtimeKey {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_REQUIRED: managed Airelay runtime identity is required")
	}
	selected, err := s.resolveManagedAgentForRuntime(ctx, runtimeKey)
	if err != nil {
		return RuntimeRoleSession{}, err
	}
	return s.resolveRuntimeRoleSessionForAgentShared(selected)
}

func (s *Service) ResolveRuntimeRoleSessionForSession(ctx context.Context, runtimeKey, sessionID string) (RuntimeRoleSession, error) {
	if runtimeKey == "" || strings.TrimSpace(runtimeKey) != runtimeKey {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_REQUIRED: managed Airelay runtime identity is required")
	}
	if model.ValidateObjectIdentifier(sessionID) != nil {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_SESSION_UNAVAILABLE: invalid durable role Session")
	}
	selected, err := s.resolveManagedAgentForRuntime(ctx, runtimeKey)
	if err != nil {
		return RuntimeRoleSession{}, err
	}
	return s.resolveRuntimeRoleSessionForAgentSelector(selected, "", sessionID, false)
}

func (s *Service) resolveRuntimeRoleSessionForAgent(selected runtimeAgentCandidate, role string) (RuntimeRoleSession, error) {
	return s.resolveRuntimeRoleSessionForAgentSelector(selected, role, "", false)
}

func (s *Service) resolveRuntimeRoleSessionForAgentShared(selected runtimeAgentCandidate) (RuntimeRoleSession, error) {
	return s.resolveRuntimeRoleSessionForAgentSelector(selected, "", "", true)
}

func (s *Service) resolveRuntimeRoleSessionForAgentSelector(selected runtimeAgentCandidate, role, sessionID string, allowShared bool) (RuntimeRoleSession, error) {
	sessions, err := durableSession.NewStoreWithDurability(s.Durability).List()
	if err != nil {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_SESSION_UNAVAILABLE: durable role Session authority is unavailable: %w", err)
	}
	matches := make([]durableSession.Record, 0, 1)
	for _, record := range sessions {
		if record.Status != durableSession.StatusActive || record.ProjectID != selected.projectID || record.SessionRef == nil || *record.SessionRef != selected.binding.SessionKey {
			continue
		}
		if sessionID != "" && record.ID != sessionID {
			continue
		}
		if role != "" && record.Role != role {
			continue
		}
		if role == "" && !runtimeRoleAllowed(record.Role) {
			continue
		}
		matches = append(matches, record)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
	if len(matches) == 0 {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_SESSION_UNAVAILABLE: no active role-bound Session matches the managed Agent and requested role")
	}
	if len(matches) != 1 && !allowShared {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_SESSION_AMBIGUOUS: multiple active role-bound Sessions match the managed Agent and requested role")
	}
	return RuntimeRoleSession{
		ProjectID: selected.projectID,
		Agent:     selected.agent,
		Binding:   selected.binding,
		Session:   matches[0],
	}, nil
}

func (s *Service) ResolveProjectLead(ctx context.Context, projectID string) (RuntimeRoleSession, error) {
	return s.resolveProjectRoleSession(ctx, projectID, durableSession.RoleLead, "Lead")
}

func (s *Service) ResolveProjectWorker(ctx context.Context, projectID string) (RuntimeRoleSession, error) {
	return s.resolveProjectRoleSession(ctx, projectID, durableSession.RoleWorker, "Worker")
}

func (s *Service) resolveProjectRoleSession(ctx context.Context, projectID, role, roleLabel string) (RuntimeRoleSession, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return RuntimeRoleSession{}, err
	}
	if s.Durability == nil || s.Durability.Local == nil {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: local session authority is unavailable")
	}
	project, err := s.ProjectRead(ctx, projectID)
	if err != nil {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: project authority is unavailable: %w", err)
	}
	if project.Status != "active" {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: project is not active")
	}
	agents, err := s.AgentList(ctx, projectID)
	if err != nil {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: managed Agent registry is unavailable: %w", err)
	}
	sessions, err := durableSession.NewStoreWithDurability(s.Durability).List()
	if err != nil {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_SESSION_UNAVAILABLE: durable role Session authority is unavailable: %w", err)
	}
	candidates := make([]RuntimeRoleSession, 0, 1)
	for _, agent := range agents {
		if !agent.Enabled || agent.Role != model.AgentRoleCoding {
			continue
		}
		binding, bound := s.agentBinding(projectID, agent.AgentID)
		if !bound {
			continue
		}
		if err := binding.Validate(); err != nil {
			continue
		}
		matches := make([]durableSession.Record, 0, 1)
		for _, record := range sessions {
			if record.Status == durableSession.StatusActive && record.ProjectID == projectID && record.Role == role && record.SessionRef != nil && *record.SessionRef == binding.SessionKey {
				matches = append(matches, record)
			}
		}
		if len(matches) > 1 {
			return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_SESSION_AMBIGUOUS: multiple active %s Sessions match managed Agent %q", roleLabel, agent.AgentID)
		}
		if len(matches) == 1 {
			candidates = append(candidates, RuntimeRoleSession{
				ProjectID: projectID,
				Agent:     agent,
				Binding:   binding,
				Session:   matches[0],
			})
		}
	}
	if len(candidates) == 0 {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: no explicitly attached %s role matches an enabled managed Agent", roleLabel)
	}
	if len(candidates) != 1 {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_AMBIGUOUS: project has multiple explicitly attached %s roles", roleLabel)
	}
	probe, err := s.Airelay.Status(ctx, candidates[0].Binding.SessionKey)
	if err != nil || !probe.ControllerReachable || strings.EqualFold(probe.State, "error") || strings.EqualFold(probe.State, "unavailable") {
		if err != nil {
			return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: %s Airelay binding is not live: %w", roleLabel, err)
		}
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: %s Airelay binding is not live", roleLabel)
	}
	candidates[0].RuntimeState = probe.State
	candidates[0].ControllerReachable = probe.ControllerReachable
	return candidates[0], nil
}

func (s *Service) ResolveLeadSession(ctx context.Context, projectID, sessionID string) (RuntimeRoleSession, error) {
	return s.resolveRoleSession(ctx, projectID, sessionID, durableSession.RoleLead, "Lead")
}

func (s *Service) ResolveWorkerSession(ctx context.Context, projectID, sessionID string) (RuntimeRoleSession, error) {
	return s.resolveRoleSession(ctx, projectID, sessionID, durableSession.RoleWorker, "Worker")
}

func (s *Service) resolveRoleSession(ctx context.Context, projectID, sessionID, role, roleLabel string) (RuntimeRoleSession, error) {
	if sessionID == "" {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_SESSION_UNAVAILABLE: %s Session is required", roleLabel)
	}
	record, err := durableSession.NewStoreWithDurability(s.Durability).Get(sessionID)
	if err != nil {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_SESSION_UNAVAILABLE: %s Session is unavailable: %w", roleLabel, err)
	}
	if record.Status != durableSession.StatusActive || record.ProjectID != projectID || record.Role != role || record.SessionRef == nil || strings.TrimSpace(*record.SessionRef) != *record.SessionRef || *record.SessionRef == "" {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_SESSION_UNAVAILABLE: %s Session binding is inactive or mismatched", roleLabel)
	}
	resolved, err := s.ResolveRuntimeRoleSession(ctx, *record.SessionRef, role)
	if err != nil {
		return RuntimeRoleSession{}, err
	}
	if resolved.ProjectID != projectID || resolved.Session.ID != sessionID {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_SESSION_UNAVAILABLE: %s Session binding is stale or mismatched", roleLabel)
	}
	return resolved, nil
}

func runtimeRoleAllowed(role string) bool {
	return durableSession.IsWorkflowRole(role)
}
