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
	label := str("Optional bounded session label.")
	label["maxLength"] = 256
	return obj(map[string]any{
		"role":  str("Server-authorized durable session role."),
		"label": label,
	}, "role")
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
	return closedOutput(map[string]any{
		"session":                 sessionIDOutputSchema(),
		"role":                    outputString(),
		"status":                  outputString(),
		"label":                   outputString(),
		"recommended_next_action": outputString(),
	}, "session", "role", "status", "recommended_next_action")
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
		Role  string  `json:"role"`
		Label *string `json:"label"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	bootstrapContext, err := authority.BootstrapSessionAuthority(ctx)
	if err != nil {
		return nil, err
	}
	started, err := s.Service.SessionStartUnbound(bootstrapContext, in.Role, in.Label)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"session":                 started.Session.ID,
		"role":                    started.Session.Role,
		"status":                  started.Session.Status,
		"recommended_next_action": "Call session/update through call with the returned session and project_id before project work.",
	}
	if started.Session.Label != nil {
		result["label"] = *started.Session.Label
	}
	return result, nil
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
