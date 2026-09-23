package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

const (
	actionRoleWorkflow      = "workflow"
	actionRolePlannerOrLead = "planner_or_lead"
)

func actionAuthorityAllowsSessionRole(_ string, sessionRole string) bool {
	return durableSession.IsWorkflowRole(sessionRole)
}

type actionAuthorityContract struct {
	Role                   string
	RequiresWorkflowPolicy bool
	LocalReceiptOnly       bool
}

func actionAuthorityContractFor(toolName string) actionAuthorityContract {
	var role string
	switch toolName {
	case "task_correction_create":
		role = durableSession.RolePlanner
	case "project_workflow_policy_adopt", "project_workflow_policy_update":
		role = durableSession.RolePlanner
	}
	return actionAuthorityContract{
		Role:                   role,
		RequiresWorkflowPolicy: role != "" && toolName != "project_workflow_policy_adopt" && toolName != "project_workflow_policy_update" && toolName != "session",
	}
}

func validateActionAuthorityRole(role string) error {
	switch role {
	case "", actionRoleWorkflow, actionRolePlannerOrLead, durableSession.RoleAdmin:
		return nil
	default:
		if durableSession.IsWorkflowRole(role) {
			return nil
		}
		return fmt.Errorf("unsupported action authority role %q", role)
	}
}

func requireActionAuthority(ctx context.Context, contract actionAuthorityContract) error {
	if _, authenticated := resolvedSessionAuthorityFromContext(ctx); authenticated {
		return nil
	}
	switch contract.Role {
	case "":
		return nil
	case durableSession.RolePlanner:
		return authority.RequirePlanner(ctx)
	case durableSession.RoleLead:
		return authority.RequireLead(ctx)
	case durableSession.RoleAdvisor:
		return authority.RequireAdvisor(ctx)
	case durableSession.RoleWorker:
		return authority.RequireWorker(ctx)
	case actionRoleWorkflow:
		if err := authority.RequirePlanner(ctx); err == nil {
			return nil
		}
		if err := authority.RequireLead(ctx); err == nil {
			return nil
		}
		if err := authority.RequireAdvisor(ctx); err == nil {
			return nil
		}
		return authority.RequireWorker(ctx)
	case actionRolePlannerOrLead:
		if err := authority.RequirePlanner(ctx); err == nil {
			return nil
		}
		return authority.RequireLead(ctx)
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

func resolvedSessionAuthorityFromContext(ctx context.Context) (resolvedSessionAuthority, bool) {
	resolved, ok := ctx.Value(sessionAuthorityContextKey{}).(resolvedSessionAuthority)
	return resolved, ok && resolved.Session.ID != ""
}

func authorizeAuthenticatedAction(ctx context.Context, actionPath string) error {
	resolved, ok := resolvedSessionAuthorityFromContext(ctx)
	if !ok {
		return fmt.Errorf("durable Session authentication is required for action %q", actionPath)
	}
	if resolved.Session.Role == durableSession.RoleAdmin {
		if actionPath == "admin" || strings.HasPrefix(actionPath, "admin/") {
			return nil
		}
		return fmt.Errorf("Admin Session may invoke only admin/* actions")
	}
	if actionPath == "admin" || strings.HasPrefix(actionPath, "admin/") {
		return fmt.Errorf("Admin Session is required for action %q", actionPath)
	}
	if !durableSession.IsWorkflowRole(resolved.Session.Role) {
		return fmt.Errorf("unsupported authenticated Session role %q", resolved.Session.Role)
	}
	return nil
}

func (s *Server) resolveSessionAuthority(ctx context.Context, record durableSession.Record, contract actionAuthorityContract) (context.Context, error) {
	if record.ID == "" {
		return ctx, nil
	}
	if record.Role == durableSession.RoleAdmin {
		if contract.Role != "" && contract.Role != durableSession.RoleAdmin {
			return nil, fmt.Errorf("Admin Session is not authorized for this action")
		}
		return withResolvedSessionAuthority(ctx, resolvedSessionAuthority{Session: record}), nil
	}
	if !durableSession.IsWorkflowRole(record.Role) {
		return nil, fmt.Errorf("unsupported persisted session role %q", record.Role)
	}
	bootstrapContext := ctx
	if elevated, err := authority.BootstrapSessionAuthority(ctx); err == nil {
		bootstrapContext = elevated
	}
	if err := requireSessionRole(bootstrapContext, record.Role); err != nil {
		return nil, fmt.Errorf("session authority is not trusted by this server: %w", err)
	}
	if contract.Role == "" && !contract.RequiresWorkflowPolicy {
		roleContext, err := withRoleAuthority(bootstrapContext, record.Role)
		if err != nil {
			return nil, err
		}
		return withResolvedSessionAuthority(roleContext, resolvedSessionAuthority{Session: record}), nil
	}
	if contract.LocalReceiptOnly {
		if record.ProjectID == "" {
			return nil, fmt.Errorf("project binding is required for local receipt action")
		}
		roleContext, err := withRoleAuthority(bootstrapContext, record.Role)
		if err != nil {
			return nil, err
		}
		return withResolvedSessionAuthority(roleContext, resolvedSessionAuthority{Session: record}), nil
	}
	if record.ProjectID == "" {
		roleContext, err := withRoleAuthority(bootstrapContext, record.Role)
		if err != nil {
			return nil, err
		}
		return withResolvedSessionAuthority(roleContext, resolvedSessionAuthority{Session: record}), nil
	}
	project, err := s.Service.ProjectRead(bootstrapContext, record.ProjectID)
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
		value, err := s.Service.ProjectWorkflowPolicyRead(bootstrapContext, record.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("workflow policy for session project %q is unavailable: %w", record.ProjectID, err)
		}
		policy = &value
	}
	roleContext, err := withRoleAuthority(bootstrapContext, record.Role)
	if err != nil {
		return nil, err
	}
	return withResolvedSessionAuthority(roleContext, resolvedSessionAuthority{
		Session: record,
		Policy:  policy,
	}), nil
}
