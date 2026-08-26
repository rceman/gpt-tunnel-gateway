package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

const actionRolePlannerOrDelivery = "planner_or_delivery"

type actionAuthorityContract struct {
	Role                   string
	RequiresWorkflowPolicy bool
	LocalReceiptOnly       bool
}

func actionAuthorityContractFor(toolName string) actionAuthorityContract {
	var role string
	switch toolName {
	case "task_correction_create":
		role = durableSession.RoleDelivery
	case "project_workflow_policy_adopt", "project_workflow_policy_update":
		role = actionRolePlannerOrDelivery
	}
	return actionAuthorityContract{
		Role:                   role,
		RequiresWorkflowPolicy: role != "" && role != actionRolePlannerOrDelivery && toolName != "project_workflow_policy_adopt" && toolName != "project_workflow_policy_update" && toolName != "session",
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

func typedSessionAuthorityContract(toolName string) (actionAuthorityContract, bool) {
	contract := actionAuthorityContractFor(toolName)
	if toolName == "session" || contract.Role == "" {
		return actionAuthorityContract{}, false
	}
	return contract, true
}

func typedSessionInputSchema(schema map[string]any) map[string]any {
	copySchema := make(map[string]any, len(schema)+1)
	for key, value := range schema {
		copySchema[key] = value
	}
	properties := make(map[string]any)
	if source, ok := schema["properties"].(map[string]any); ok {
		for key, value := range source {
			properties[key] = value
		}
	}
	properties["session_id"] = str("Durable project-bound Planner or Delivery session authority.")
	properties["session_id"].(map[string]any)["pattern"] = sessionIDPattern
	copySchema["properties"] = properties
	required := append([]string{}, stringList(schema["required"])...)
	for _, key := range required {
		if key == "session_id" {
			copySchema["required"] = required
			return copySchema
		}
	}
	copySchema["required"] = append(required, "session_id")
	return copySchema
}

func (s *Server) resolveTypedSessionAuthority(ctx context.Context, toolName string, schema map[string]any, raw json.RawMessage) (context.Context, json.RawMessage, error) {
	contract, required := typedSessionAuthorityContract(toolName)
	if !required {
		return ctx, raw, nil
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil || args == nil {
		return nil, nil, fmt.Errorf("session_id is required for authority-sensitive typed action")
	}
	value, ok := args["session_id"]
	if !ok {
		return nil, nil, fmt.Errorf("session_id is required for authority-sensitive typed action")
	}
	var sessionID string
	if err := json.Unmarshal(value, &sessionID); err != nil || sessionID == "" {
		return nil, nil, fmt.Errorf("session_id must be a non-empty durable session identifier")
	}
	record, err := s.activeSession(sessionID)
	if err != nil {
		return nil, nil, fmt.Errorf("typed action session authority is invalid: %w", err)
	}
	resolved, err := s.resolveSessionAuthority(ctx, record, contract)
	if err != nil {
		return nil, nil, err
	}
	bound, err := inheritSessionProject(schema, record.ProjectID, raw)
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(bound, &args); err != nil || args == nil {
		return nil, nil, fmt.Errorf("typed action arguments must be an object")
	}
	delete(args, "session_id")
	executable, err := json.Marshal(args)
	if err != nil {
		return nil, nil, fmt.Errorf("typed action arguments could not be normalized: %w", err)
	}
	return resolved, executable, nil
}

// sessionIDFromRaw extracts only the durable-session locator. It does not
// treat the identifier's prefix as authority; resolveSessionAuthority still
// validates the persisted record and rebinds the exact stored role.
func sessionIDFromRaw(raw json.RawMessage) (string, bool) {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil || args == nil {
		return "", false
	}
	value, ok := args["session_id"]
	if !ok {
		return "", false
	}
	var sessionID string
	if err := json.Unmarshal(value, &sessionID); err != nil || sessionID == "" {
		return "", false
	}
	return sessionID, true
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
	bootstrapContext := ctx
	if elevated, err := authority.BootstrapSessionAuthority(ctx); err == nil {
		bootstrapContext = elevated
	}
	if err := requireSessionRole(bootstrapContext, record.Role); err != nil {
		return nil, fmt.Errorf("session authority is not trusted by this server: %w", err)
	}
	if contract.Role != "" && contract.Role != actionRolePlannerOrDelivery && record.Role != contract.Role {
		return nil, fmt.Errorf("session role %q is not authorized for this action; required %q", record.Role, contract.Role)
	}
	if contract.Role == actionRolePlannerOrDelivery && record.Role != durableSession.RolePlanner && record.Role != durableSession.RoleDelivery {
		return nil, fmt.Errorf("session role %q is not authorized for this action", record.Role)
	}
	if contract.LocalReceiptOnly {
		if record.ProjectID == "" {
			return nil, fmt.Errorf("project binding is required for local receipt action")
		}
		roleContext := bootstrapContext
		switch record.Role {
		case durableSession.RolePlanner:
			roleContext = authority.WithPlanner(roleContext)
		case durableSession.RoleDelivery:
			roleContext = authority.WithDelivery(roleContext)
		}
		return withResolvedSessionAuthority(roleContext, resolvedSessionAuthority{Session: record}), nil
	}
	if record.ProjectID == "" {
		roleContext := bootstrapContext
		switch record.Role {
		case durableSession.RolePlanner:
			roleContext = authority.WithPlanner(roleContext)
		case durableSession.RoleDelivery:
			roleContext = authority.WithDelivery(roleContext)
		default:
			return nil, fmt.Errorf("session role %q is invalid", record.Role)
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
	roleContext := bootstrapContext
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
