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
			binding, bound := s.Config.ResolveAgentBinding(projectID, agent.AgentID)
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

func (s *Service) resolveRuntimeRoleSessionForAgent(selected runtimeAgentCandidate, role string) (RuntimeRoleSession, error) {
	sessions, err := durableSession.NewStoreWithDurability(s.Durability).List()
	if err != nil {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_SESSION_UNAVAILABLE: durable role Session authority is unavailable: %w", err)
	}
	matches := make([]durableSession.Record, 0, 1)
	for _, record := range sessions {
		if record.Status != durableSession.StatusActive || record.ProjectID != selected.projectID || record.SessionRef == nil || *record.SessionRef != selected.binding.SessionKey {
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
	if len(matches) != 1 {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_SESSION_AMBIGUOUS: multiple active role-bound Sessions match the managed Agent and requested role")
	}
	return RuntimeRoleSession{
		ProjectID: selected.projectID,
		Agent:     selected.agent,
		Binding:   selected.binding,
		Session:   matches[0],
	}, nil
}

func (s *Service) ResolveProjectWorker(ctx context.Context, projectID string) (RuntimeRoleSession, error) {
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
		binding, bound := s.Config.ResolveAgentBinding(projectID, agent.AgentID)
		if !bound {
			continue
		}
		if err := binding.Validate(); err != nil {
			continue
		}
		matches := make([]durableSession.Record, 0, 1)
		for _, record := range sessions {
			if record.Status == durableSession.StatusActive && record.ProjectID == projectID && record.Role == durableSession.RoleWorker && record.SessionRef != nil && *record.SessionRef == binding.SessionKey {
				matches = append(matches, record)
			}
		}
		if len(matches) > 1 {
			return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_SESSION_AMBIGUOUS: multiple active Worker Sessions match managed Agent %q", agent.AgentID)
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
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: no explicitly attached Worker role matches an enabled managed Agent")
	}
	if len(candidates) != 1 {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_AMBIGUOUS: project has multiple explicitly attached Worker roles")
	}
	probe, err := s.Airelay.Status(ctx, candidates[0].Binding.SessionKey)
	if err != nil || !probe.ControllerReachable || strings.EqualFold(probe.State, "error") || strings.EqualFold(probe.State, "unavailable") {
		if err != nil {
			return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: Worker Airelay binding is not live: %w", err)
		}
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: Worker Airelay binding is not live")
	}
	candidates[0].RuntimeState = probe.State
	candidates[0].ControllerReachable = probe.ControllerReachable
	return candidates[0], nil
}

func (s *Service) ResolveWorkerSession(ctx context.Context, projectID, sessionID string) (RuntimeRoleSession, error) {
	if sessionID == "" {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_SESSION_UNAVAILABLE: Worker Session is required")
	}
	record, err := durableSession.NewStoreWithDurability(s.Durability).Get(sessionID)
	if err != nil {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_SESSION_UNAVAILABLE: Worker Session is unavailable: %w", err)
	}
	if record.Status != durableSession.StatusActive || record.ProjectID != projectID || record.Role != durableSession.RoleWorker || record.SessionRef == nil || strings.TrimSpace(*record.SessionRef) != *record.SessionRef || *record.SessionRef == "" {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_SESSION_UNAVAILABLE: Worker Session binding is inactive or mismatched")
	}
	resolved, err := s.ResolveRuntimeRoleSession(ctx, *record.SessionRef, durableSession.RoleWorker)
	if err != nil {
		return RuntimeRoleSession{}, err
	}
	if resolved.ProjectID != projectID || resolved.Session.ID != sessionID {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_SESSION_UNAVAILABLE: Worker Session binding is stale or mismatched")
	}
	return resolved, nil
}

func runtimeRoleAllowed(role string) bool {
	switch role {
	case durableSession.RolePlanner, durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker:
		return true
	default:
		return false
	}
}
