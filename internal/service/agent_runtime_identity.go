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
	ProjectID string
	Agent     model.Agent
	Binding   config.AgentBinding
	Session   durableSession.Record
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
	if s.Durability == nil || s.Durability.Local == nil {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: local session authority is unavailable")
	}
	projectIDs, err := s.EffectiveProjectIDs()
	if err != nil {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: project registry is unavailable: %w", err)
	}
	type candidate struct {
		projectID string
		agent     model.Agent
		binding   config.AgentBinding
	}
	candidates := make([]candidate, 0, len(projectIDs))
	for _, projectID := range projectIDs {
		project, projectErr := s.ProjectRead(ctx, projectID)
		if projectErr != nil {
			return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: project authority is unavailable: %w", projectErr)
		}
		if project.Status != "active" {
			continue
		}
		agents, listErr := s.AgentList(ctx, projectID)
		if listErr != nil {
			return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: managed Agent registry is unavailable: %w", listErr)
		}
		for _, agent := range agents {
			if !agent.Enabled || agent.Role != model.AgentRoleCoding {
				continue
			}
			binding, bound := s.Config.ResolveAgentBinding(projectID, agent.AgentID)
			if !bound || binding.Validate() != nil || binding.SessionKey != runtimeKey {
				continue
			}
			candidates = append(candidates, candidate{
				projectID: projectID,
				agent:     agent,
				binding:   binding,
			})
		}
	}
	if len(candidates) == 0 {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_UNAVAILABLE: no enabled managed Agent matches the calling runtime")
	}
	if len(candidates) != 1 {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_IDENTITY_AMBIGUOUS: calling runtime matches multiple enabled managed Agents")
	}
	selected := candidates[0]

	sessions, err := durableSession.NewStoreWithDurability(s.Durability).List()
	if err != nil {
		return RuntimeRoleSession{}, fmt.Errorf("RUNTIME_SESSION_UNAVAILABLE: durable role Session authority is unavailable: %w", err)
	}
	matches := make([]durableSession.Record, 0, 1)
	for _, record := range sessions {
		if record.Status != durableSession.StatusActive || record.ProjectID != selected.projectID || record.SessionRef == nil || *record.SessionRef != runtimeKey {
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

func runtimeRoleAllowed(role string) bool {
	switch role {
	case durableSession.RolePlanner, durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker:
		return true
	default:
		return false
	}
}
