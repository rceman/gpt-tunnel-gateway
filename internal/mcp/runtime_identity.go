package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
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
	if strings.HasPrefix(action, "agent/") {
		return ""
	}
	switch action {
	case "task/current", "task/submit-code", "task/submit-tests", "task/submit-rebase", "task/read", "task/guide":
		return durableSession.RoleWorker
	case "task/status", "task/review":
		return durableSession.RoleLead
	}
	switch entry.AuthorityRole {
	case durableSession.RolePlanner, durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker:
		return entry.AuthorityRole
	default:
		return ""
	}
}

func runtimeActionAcceptsManagedRole(action string) bool {
	return strings.HasPrefix(action, "agent/")
}

func durableRoleRequiresRuntime(role string) bool {
	return durableSession.WorkflowRoleRequiresRuntime(role)
}

type runtimeSessionResolution struct {
	Session durableSession.Record
	AgentID string
	Role    string
}

func (s *Server) resolveRuntimeSession(ctx context.Context, runtimeKey, action string, entry genericActionEntry, raw json.RawMessage) (runtimeSessionResolution, error) {
	role := runtimeRoleForAction(action, entry)
	if role == "" && !runtimeActionAcceptsManagedRole(action) {
		return runtimeSessionResolution{}, fmt.Errorf("RUNTIME_ROLE_UNAUTHORIZED: action is not available to a managed runtime")
	}
	var resolved service.RuntimeRoleSession
	var err error
	if strings.HasPrefix(action, "agent/") {
		if action == "agent/tail" {
			var input struct {
				Session string `json:"session"`
			}
			if len(raw) == 0 {
				resolved, err = s.Service.ResolveRuntimeAgentSession(ctx, runtimeKey)
			} else if err := json.Unmarshal(raw, &input); err != nil {
				return runtimeSessionResolution{}, err
			} else if input.Session != "" {
				resolved, err = s.Service.ResolveRuntimeRoleSessionForSession(ctx, runtimeKey, input.Session)
				if err != nil {
					resolved, err = s.Service.ResolveRuntimeAgentSession(ctx, runtimeKey)
				}
			} else {
				resolved, err = s.Service.ResolveRuntimeRoleSession(ctx, runtimeKey, "")
			}
		} else {
			resolved, err = s.Service.ResolveRuntimeAgentSession(ctx, runtimeKey)
		}
	} else {
		resolved, err = s.Service.ResolveRuntimeRoleSession(ctx, runtimeKey, role)
	}
	if err != nil {
		return runtimeSessionResolution{}, err
	}
	return runtimeSessionResolution{
		Session: resolved.Session,
		AgentID: resolved.Agent.AgentID,
		Role:    resolved.Session.Role,
	}, nil
}
