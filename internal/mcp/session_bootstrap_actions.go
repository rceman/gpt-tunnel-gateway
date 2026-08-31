package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

const globalWorkflowRevision = "gpt-tunnel-workflow-v1"

func globalWorkflowRules() map[string]any {
	return map[string]any{
		"name":     "gpt-tunnel-workflow",
		"revision": globalWorkflowRevision,
		"content":  "Bootstrap, bind one immutable project Session, read project/status, then perform project work; inspect schema only when a contract is unknown.",
	}
}

func globalWorkflowDigest() string {
	b, _ := json.Marshal(globalWorkflowRules())
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func sessionStartPublicInputSchema() map[string]any {
	ref := str("Optional bounded caller reference; Agent sessions require the exact Airelay session key.")
	ref["minLength"] = 1
	ref["maxLength"] = 256
	gateway := str("Canonical registered Gateway key.")
	gateway["minLength"] = 1
	project := str("Canonical registered project key.")
	project["minLength"] = 1
	role := str("Server-authorized durable session role.")
	role["minLength"] = 1
	return obj(map[string]any{
		"gateway": gateway,
		"project": project,
		"role":    role,
		"ref":     ref,
	}, "gateway", "project", "role")
}

func sessionUpdatePublicInputSchema() map[string]any {
	sessionID := str("Existing durable session identifier.")
	sessionID["pattern"] = sessionIDPattern
	return obj(map[string]any{
		"session":    sessionID,
		"project_id": str("Canonical registered project identifier."),
		"ref":        str("Optional caller reference."),
	}, "session", "project_id")
}

func sessionStartPublicOutputSchema() map[string]any {
	gateway := closedOutput(map[string]any{
		"key":   outputString(),
		"label": outputString(),
	}, "key")
	project := closedOutput(map[string]any{
		"key":  outputString(),
		"name": outputString(),
	}, "key", "name")
	rule := closedOutput(map[string]any{
		"key":      outputString(),
		"revision": outputInteger(),
		"text":     outputString(),
	}, "key", "revision", "text")
	return closedOutput(map[string]any{
		"session": sessionIDOutputSchema(),
		"gateway": gateway,
		"project": project,
		"role":    outputString(),
		"ref":     outputString(),
		"rules": closedOutput(map[string]any{
			"digest": outputString(),
			"items":  outputArray(rule),
		}, "digest", "items"),
	}, "session", "gateway", "project", "role", "rules")
}

func sessionUpdatePublicOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"session":                        sessionIDOutputSchema(),
		"project_id":                     outputString(),
		"rules":                          workflowPolicyOutputSchema(),
		"project_rules_revision":         outputInteger(),
		"project_rules_digest":           outputString(),
		"rules_acknowledgement_required": outputBoolean(),
		"project_rules_acknowledged":     outputBoolean(),
	}, "session", "project_id", "rules", "project_rules_revision", "project_rules_digest", "rules_acknowledgement_required", "project_rules_acknowledged")
}

func (s *Server) sessionStartPublic(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		Gateway string  `json:"gateway"`
		Project string  `json:"project"`
		Role    string  `json:"role"`
		Ref     *string `json:"ref"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if in.Gateway == "" || in.Gateway != s.Service.Config.GatewayID {
		return nil, fmt.Errorf("unknown gateway %q", in.Gateway)
	}
	if in.Project == "" || in.Role == "" {
		return nil, fmt.Errorf("gateway, project, and role are required")
	}
	resolution, err := s.Service.EffectiveProjectSnapshot()
	if err != nil {
		return nil, fmt.Errorf("project registry unavailable: %w", err)
	}
	project, ok := resolution.Projects[in.Project]
	if !ok {
		return nil, fmt.Errorf("unknown project %q", in.Project)
	}
	if in.Role == durableSession.RoleAgent && (in.Ref == nil || *in.Ref == "") {
		return nil, fmt.Errorf("Agent session ref is required")
	}
	bootstrapContext, err := authority.BootstrapSessionAuthority(ctx)
	if err != nil {
		return nil, err
	}
	var sessionContext context.Context
	switch in.Role {
	case durableSession.RolePlanner:
		sessionContext = authority.WithPlanner(bootstrapContext)
	case durableSession.RoleDelivery:
		sessionContext = authority.WithDelivery(bootstrapContext)
	case durableSession.RoleAgent:
		sessionContext = authority.WithAgent(bootstrapContext)
	default:
		return nil, fmt.Errorf("unsupported session role %q", in.Role)
	}
	started, err := s.Service.SessionStart(sessionContext, service.SessionStartInput{
		ProjectID:   in.Project,
		ProjectCode: project.ProjectCode,
		Role:        in.Role,
		SessionType: durableSession.SessionTypeChatGPT,
		SessionRef:  in.Ref,
	})
	if err != nil {
		return nil, err
	}
	rules := publicSessionRules()
	result := map[string]any{
		"session": started.Session.ID,
		"gateway": map[string]any{"key": in.Gateway},
		"project": map[string]any{"key": in.Project, "name": in.Project},
		"role":    started.Session.Role,
		"rules":   rules,
	}
	if started.Session.SessionRef != nil {
		result["ref"] = *started.Session.SessionRef
	}
	return result, nil
}

func publicSessionRules() map[string]any {
	content, _ := globalWorkflowRules()["content"].(string)
	return map[string]any{
		"digest": globalWorkflowDigest(),
		"items": []map[string]any{{
			"key":      "workflow",
			"revision": 1,
			"text":     content,
		}},
	}
}

func (s *Server) sessionUpdatePublic(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		Session   string  `json:"session"`
		ProjectID string  `json:"project_id"`
		Ref       *string `json:"ref"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	record, err := s.activeSession(in.Session)
	if err != nil {
		return nil, err
	}
	roleContext, err := existingSessionRoleContext(ctx, record.Role)
	if err != nil {
		return nil, err
	}
	var bound service.SessionResult
	if record.ProjectID == in.ProjectID {
		bound, err = s.Service.SessionUpdate(roleContext, service.SessionUpdateInput{SessionID: in.Session, SessionRef: in.Ref})
	} else {
		bound, err = s.Service.SessionBind(roleContext, service.SessionBindInput{SessionID: in.Session, ProjectID: in.ProjectID, SessionRef: in.Ref})
	}
	if err != nil {
		return nil, err
	}
	policy, err := s.Service.ProjectWorkflowPolicyReadFast(ctx, bound.Session.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("session project rules unavailable: %w", err)
	}
	digest := digestJSON(policy)
	updated, err := durableSession.NewStore(s.Service.Config.StateDir).AcknowledgeRules(in.Session, globalWorkflowRevision, globalWorkflowDigest(), policy.Revision, digest)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"session":                        updated.ID,
		"project_id":                     updated.ProjectID,
		"rules":                          policy,
		"project_rules_revision":         updated.ProjectRulesRevision,
		"project_rules_digest":           updated.ProjectRulesDigest,
		"rules_acknowledgement_required": false,
		"project_rules_acknowledged":     updated.ProjectRulesRevision > 0 && updated.ProjectRulesDigest != "",
	}, nil
}

func workflowWithDigest(workflow map[string]any, digest string) map[string]any {
	result := make(map[string]any, len(workflow)+1)
	for key, value := range workflow {
		result[key] = value
	}
	result["digest"] = digest
	return result
}

func (s *Server) sessionBindAction(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		ProjectID string  `json:"project_id"`
		Ref       *string `json:"ref"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	return s.Service.SessionBind(ctx, service.SessionBindInput{SessionID: service.AgentSessionID(ctx), ProjectID: in.ProjectID, SessionRef: in.Ref})
}

func (s *Server) rulesReadAction(ctx context.Context, raw json.RawMessage) (any, error) {
	id := service.AgentSessionID(ctx)
	if id == "" {
		return nil, fmt.Errorf("SESSION_REQUIRED: provide the public session field")
	}
	info, err := s.Service.SessionInfo(ctx, id)
	if err != nil {
		return nil, err
	}
	if info.Session.ProjectID == "" {
		return nil, fmt.Errorf("PROJECT_BINDING_REQUIRED: bind the session before reading project rules")
	}
	policy, err := s.Service.ProjectWorkflowPolicyReadFast(ctx, info.Session.ProjectID)
	if err != nil {
		return nil, err
	}
	digest := digestJSON(policy)
	updated, err := durableSession.NewStore(s.Service.Config.StateDir).AcknowledgeRules(id, globalWorkflowRevision, globalWorkflowDigest(), policy.Revision, digest)
	if err != nil {
		return nil, err
	}
	return map[string]any{"rules": policy, "session": updated.ID, "project_rules_revision": updated.ProjectRulesRevision, "project_rules_digest": updated.ProjectRulesDigest}, nil
}

func digestJSON(value any) string {
	b, _ := json.Marshal(value)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *Server) validateSessionRules(ctx context.Context, record durableSession.Record, action string) error {
	if record.ProjectID == "" || record.GlobalRulesRevision == "" || action == "rules/read" || action == "session/bind" || action == "session/info" || action == "session/end" || action == "session/update" || action == "project/list" {
		return nil
	}
	if record.GlobalRulesRevision != globalWorkflowRevision || record.GlobalRulesDigest != globalWorkflowDigest() {
		return fmt.Errorf("RULES_REFRESH_REQUIRED: global workflow rules changed")
	}
	policy, err := s.Service.CachedProjectWorkflowPolicy(record.ProjectID)
	if err != nil {
		return fmt.Errorf("RULES_REFRESH_REQUIRED: project rules unavailable")
	}
	if record.ProjectRulesRevision != policy.Revision || record.ProjectRulesDigest != digestJSON(policy) {
		return fmt.Errorf("RULES_REFRESH_REQUIRED: read current project rules")
	}
	return nil
}
