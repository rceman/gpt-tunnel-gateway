package mcp

import (
	"context"
	"fmt"
	"strings"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

type managedRuntimeContextKey struct{}

type managedRuntimeIdentity struct {
	AgentID string
	Role    string
}

func withManagedRuntimeIdentity(ctx context.Context, identity managedRuntimeIdentity) context.Context {
	return context.WithValue(ctx, managedRuntimeContextKey{}, identity)
}

func hasManagedRuntimeIdentity(ctx context.Context) bool {
	_, ok := ctx.Value(managedRuntimeContextKey{}).(managedRuntimeIdentity)
	return ok
}

func managedRuntimeAgentID(ctx context.Context) string {
	identity, _ := ctx.Value(managedRuntimeContextKey{}).(managedRuntimeIdentity)
	return identity.AgentID
}

func runtimeRoleForAction(action string, entry genericActionEntry) string {
	switch action {
	case "task/current", "task/submit-code", "task/submit-tests", "task/submit-rebase", "task/read", "task/guide", "agent/status":
		return durableSession.RoleWorker
	case "task/status", "task/review":
		return durableSession.RoleLead
	}
	switch entry.AuthorityRole {
	case durableSession.RolePlanner, durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker:
		return entry.AuthorityRole
	case durableSession.RoleAgent:
		return durableSession.RoleWorker
	default:
		return ""
	}
}

func runtimeActionAcceptsManagedRole(action string) bool {
	return strings.HasPrefix(action, "agent/")
}

func durableRoleRequiresRuntime(role string) bool {
	switch role {
	case durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker:
		return true
	default:
		return false
	}
}

type runtimeSessionResolution struct {
	Session durableSession.Record
	AgentID string
	Role    string
}

func (s *Server) resolveRuntimeSession(ctx context.Context, runtimeKey, action string, entry genericActionEntry) (runtimeSessionResolution, error) {
	role := runtimeRoleForAction(action, entry)
	if role == "" && !runtimeActionAcceptsManagedRole(action) {
		return runtimeSessionResolution{}, fmt.Errorf("RUNTIME_ROLE_UNAUTHORIZED: action is not available to a managed runtime")
	}
	resolved, err := s.Service.ResolveRuntimeRoleSession(ctx, runtimeKey, role)
	if err != nil {
		return runtimeSessionResolution{}, err
	}
	return runtimeSessionResolution{
		Session: resolved.Session,
		AgentID: resolved.Agent.AgentID,
		Role:    resolved.Session.Role,
	}, nil
}
