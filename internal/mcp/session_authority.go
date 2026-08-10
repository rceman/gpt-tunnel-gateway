package mcp

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

const actionRolePlannerOrDelivery = "planner_or_delivery"

type actionAuthorityContract struct {
	Role                   string
	RequiresWorkflowPolicy bool
}

func actionAuthorityContractFor(toolName string) actionAuthorityContract {
	var role string
	switch toolName {
	case "task_correction_create":
		role = durableSession.RoleDelivery
	case "delivery_handoff_publish", "delivery_handoff_cancel", "delivery_handoff_supersede", "planner_report_acknowledge", "planner_report_next":
		role = durableSession.RolePlanner
	case "delivery_handoff_acknowledge", "delivery_handoff_next", "planner_report_publish":
		role = durableSession.RoleDelivery
	case "project_onboard", "project_onboard_recover", "project_workflow_policy_adopt", "project_workflow_policy_update", "session":
		role = actionRolePlannerOrDelivery
	}
	return actionAuthorityContract{
		Role:                   role,
		RequiresWorkflowPolicy: role != "" && role != actionRolePlannerOrDelivery && toolName != "project_workflow_policy_adopt" && toolName != "project_workflow_policy_update" && toolName != "project_onboard" && toolName != "project_onboard_recover" && toolName != "session",
	}
}

func validateActionAuthorityRole(role string) error {
	switch role {
	case "", durableSession.RolePlanner, durableSession.RoleDelivery, actionRolePlannerOrDelivery:
		return nil
	default:
		return fmt.Errorf("unsupported action authority role %q", role)
	}
}

func requireActionAuthority(ctx context.Context, contract actionAuthorityContract) error {
	switch contract.Role {
	case "":
		return nil
	case durableSession.RolePlanner:
		return authority.RequirePlanner(ctx)
	case durableSession.RoleDelivery:
		return authority.RequireDelivery(ctx)
	case actionRolePlannerOrDelivery:
		return authority.RequirePlannerOrDelivery(ctx)
	default:
		return fmt.Errorf("unsupported action authority role %q", contract.Role)
	}
}

type sessionAuthorityContextKey struct{}

type resolvedSessionAuthority struct {
	Session durableSession.Record
	Policy  *model.ProjectWorkflowPolicy
}

func withResolvedSessionAuthority(ctx context.Context, resolved resolvedSessionAuthority) context.Context {
	return context.WithValue(ctx, sessionAuthorityContextKey{}, resolved)
}

func (s *Server) resolveSessionAuthority(ctx context.Context, record durableSession.Record, contract actionAuthorityContract) (context.Context, error) {
	if record.ID == "" {
		return ctx, nil
	}
	if contract.Role == "" && !contract.RequiresWorkflowPolicy {
		return ctx, nil
	}
	if err := requireSessionRole(ctx, record.Role); err != nil {
		return nil, fmt.Errorf("session authority is not trusted by this server: %w", err)
	}
	if contract.Role != "" && contract.Role != actionRolePlannerOrDelivery && record.Role != contract.Role {
		return nil, fmt.Errorf("session role %q is not authorized for this action; required %q", record.Role, contract.Role)
	}
	if contract.Role == actionRolePlannerOrDelivery && record.Role != durableSession.RolePlanner && record.Role != durableSession.RoleDelivery {
		return nil, fmt.Errorf("session role %q is not authorized for this action", record.Role)
	}
	project, err := s.Service.ProjectRead(ctx, record.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("session project %q is not durably registered: %w", record.ProjectID, err)
	}
	if err := model.ValidateProject(project); err != nil {
		return nil, fmt.Errorf("session project %q is invalid: %w", record.ProjectID, err)
	}
	if project.ID != record.ProjectID || project.Status != "active" {
		return nil, fmt.Errorf("session project %q is not active", record.ProjectID)
	}
	if _, err := s.Service.EffectiveProjectConfig(record.ProjectID); err != nil {
		return nil, fmt.Errorf("session project %q has no effective configuration: %w", record.ProjectID, err)
	}
	var policy *model.ProjectWorkflowPolicy
	if contract.RequiresWorkflowPolicy {
		value, err := s.Service.ProjectWorkflowPolicyRead(ctx, record.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("workflow policy for session project %q is unavailable: %w", record.ProjectID, err)
		}
		policy = &value
	}
	roleContext := ctx
	switch record.Role {
	case durableSession.RolePlanner:
		roleContext = authority.WithPlanner(roleContext)
	case durableSession.RoleDelivery:
		roleContext = authority.WithDelivery(roleContext)
	default:
		return nil, fmt.Errorf("session role %q is invalid", record.Role)
	}
	return withResolvedSessionAuthority(roleContext, resolvedSessionAuthority{
		Session: record,
		Policy:  policy,
	}), nil
}
