package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) addCoreTools(add toolAdder) {
	add("bootstrap", "Return compact Gateway readiness, project-code discovery, and bootstrap workflow guidance.", bootstrapInputSchema(), func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.bootstrapPublic(ctx, raw)
	})
	add("system_ping", "Return gateway identity and time.", obj(map[string]any{}), func(ctx context.Context, raw json.RawMessage) (any, error) {
		return map[string]any{"service": "gpt-tunnel-gatewayd", "version": "0.6.11", "gateway_id": s.Service.Config.GatewayID, "time": time.Now().UTC()}, nil
	})
	add("session", "Create and manage explicit durable project-bound sessions.", sessionInputSchema(), func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.sessionAction(ctx, raw)
	})
	add("gateway_capabilities", "Describe configured limits, projects, and transport.", obj(map[string]any{}), func(ctx context.Context, raw json.RawMessage) (any, error) {
		ids, err := s.Service.EffectiveProjectIDs()
		if err != nil {
			return nil, err
		}
		return map[string]any{"gateway_id": s.Service.Config.GatewayID, "listen_addr": s.Service.Config.ListenAddr, "projects": ids, "hub_protocol_root": hub.ProtocolRoot, "hub_repository_url": s.Service.Config.Hub.RepositoryURL, "hub_branch": s.Service.Config.Hub.Branch, "hub_managed_root": hub.ManagedRoot(s.Service.Config), "airelay_control_only": true, "generic_shell_available": false}, nil
	})
	limit := integer("Maximum projects", 1, service.MaxPublicCollectionLimit)
	limit["default"] = service.DefaultPublicCollectionLimit
	add("project_list", "List bounded durable hub projects with deterministic continuation. New next_cursor values are compact server-owned tokens of at most 8 safe characters; legacy cursors remain input-compatible.", obj(map[string]any{"limit": limit, "cursor": str("Server-owned continuation cursor; new values are <=8 safe characters and legacy values are accepted")}), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.CollectionPageInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.ProjectListPage(ctx, in)
	})
	add("project_read", "Read one durable project.", obj(map[string]any{"project_id": str("Project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "project_id")
		if e != nil {
			return nil, e
		}
		return s.Service.ProjectRead(ctx, id)
	})
	add("project_identifiers_read", "Read the immutable compact-ID allocation record for a durable project.", obj(map[string]any{"project_id": str("Project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "project_id")
		if e != nil {
			return nil, e
		}
		return s.Service.ProjectIdentifiersRead(ctx, id)
	})
	projectCode := str("Three-letter uppercase project code")
	projectCode["pattern"] = "^[A-Z]{3}$"
	add("project_identifiers_adopt", "Atomically adopt a unique immutable compact-ID project code and initialize its counters; this does not switch task, ADR, or run creation to compact IDs.", obj(map[string]any{"project_id": str("Project identifier"), "project_code": projectCode, "expected_hub_revision": str("Optimistic hub revision")}, "project_id", "project_code"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.ProjectIdentifiersAdoptInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		identifiers, operation, e := s.Service.ProjectIdentifiersAdopt(ctx, in)
		if e != nil {
			return nil, e
		}
		return map[string]any{"identifiers": identifiers, "operation": operation}, nil
	})
	add("project_workflow_policy_read", "Read the durable revisioned project workflow policy.", obj(map[string]any{"project_id": str("Project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "project_id")
		if e != nil {
			return nil, e
		}
		return s.Service.ProjectWorkflowPolicyRead(ctx, id)
	})
	policyWriteSchema := obj(map[string]any{"policy": workflowPolicyOutputSchema(), "expected_hub_revision": str("Optimistic hub revision")}, "policy")
	add("project_workflow_policy_adopt", "Planner or Delivery adoption of the durable project workflow policy through trusted server-owned authority; unavailable authority fails closed.", policyWriteSchema, func(ctx context.Context, raw json.RawMessage) (any, error) {
		if err := service.RequireWorkflowPolicyAuthority(ctx); err != nil {
			return nil, err
		}
		var in service.ProjectWorkflowPolicyInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		policy, operation, e := s.Service.ProjectWorkflowPolicyAdopt(ctx, in)
		return map[string]any{"policy": policy, "operation": operation}, e
	})
	add("project_workflow_policy_update", "Planner or Delivery revisioned update of the durable project workflow policy through trusted server-owned authority; unavailable authority fails closed.", policyWriteSchema, func(ctx context.Context, raw json.RawMessage) (any, error) {
		if err := service.RequireWorkflowPolicyAuthority(ctx); err != nil {
			return nil, err
		}
		var in service.ProjectWorkflowPolicyInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		policy, operation, e := s.Service.ProjectWorkflowPolicyUpdate(ctx, in)
		return map[string]any{"policy": policy, "operation": operation}, e
	})
}
