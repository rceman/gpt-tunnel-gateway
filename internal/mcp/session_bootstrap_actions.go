package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/session"
)

const globalWorkflowRevision = "gpt-tunnel-workflow-v1"

func globalWorkflowRules() map[string]any {
	return map[string]any{
		"name":     "gpt-tunnel-workflow",
		"revision": globalWorkflowRevision,
		"content":  "Inspect schema, bind one project, read project rules, then perform project work.",
	}
}

func globalWorkflowDigest() string {
	b, _ := json.Marshal(globalWorkflowRules())
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func sessionStartPublicInputSchema() map[string]any {
	return obj(map[string]any{
		"role":  str("Server-authorized session role."),
		"label": str("Optional bounded session label."),
	}, "role")
}

func sessionUpdatePublicInputSchema() map[string]any {
	sessionID := str("Existing durable session identifier.")
	sessionID["pattern"] = `^(?:S|SP|SD|SA|SW)-[0-9ABCDEFGHJKMNPQRSTVWXYZ]{8}$`
	return obj(map[string]any{
		"session":    sessionID,
		"project_id": str("Canonical registered project identifier."),
		"ref":        str("Optional caller reference."),
	}, "session", "project_id")
}

func sessionStartPublicOutputSchema() map[string]any {
	project := closedOutput(map[string]any{
		"project_id": outputString(), "project_code": outputString(), "status": outputString(), "default_branch": outputString(),
	}, "project_id", "status")
	rules := map[string]any{"type": "object", "additionalProperties": true}
	return closedOutput(map[string]any{
		"session":    sessionIDOutputSchema(),
		"rules":      rules,
		"workflow":   rules,
		"projects":   outputArray(project),
		"next_steps": outputArray(outputString()),
	}, "session", "rules", "projects", "next_steps")
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
	trusted, err := authority.BootstrapSessionAuthority(ctx)
	if err != nil {
		return nil, err
	}
	started, err := s.Service.SessionStartUnbound(trusted, in.Role, in.Label)
	if err != nil {
		return nil, err
	}
	workflow := globalWorkflowRules()
	digest := globalWorkflowDigest()
	started.Session, err = session.NewStore(s.Service.Config.StateDir).AcknowledgeRules(started.Session.ID, globalWorkflowRevision, digest, 0, "")
	if err != nil {
		return nil, err
	}
	// Bootstrap must preserve the project summary contract, but it must not
	// refresh or read the Hub. The effective local registry is sufficient for
	// the bounded identity/default-branch summary; durable project details are
	// read only after the caller binds a project.
	resolution, err := s.Service.EffectiveProjectSnapshot()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(resolution.Projects))
	for id := range resolution.Projects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if max := s.Service.Config.MaxListItems; max > 0 && len(ids) > max {
		ids = ids[:max]
	}
	projectSummaries := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		project := resolution.Projects[id]
		summary := map[string]any{
			"project_id":     id,
			"status":         "active",
			"default_branch": project.DefaultBranch,
		}
		if project.ProjectCode != "" {
			summary["project_code"] = project.ProjectCode
		}
		projectSummaries = append(projectSummaries, summary)
	}
	return map[string]any{
		"session":  started.Session.ID,
		"rules":    workflowWithDigest(workflow, digest),
		"workflow": workflowWithDigest(workflow, digest),
		"projects": projectSummaries,
		"next_steps": []string{
			"inspect schema",
			"bind one project through session_update",
			"read project rules through rules/read",
		},
	}, nil
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
	updated, err := session.NewStore(s.Service.Config.StateDir).AcknowledgeRules(in.Session, globalWorkflowRevision, globalWorkflowDigest(), policy.Revision, digest)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"session":                        updated.ID,
		"project_id":                     updated.ProjectID,
		"rules":                          policy,
		"project_rules_revision":         updated.ProjectRulesRevision,
		"project_rules_digest":           updated.ProjectRulesDigest,
		"rules_acknowledgement_required": true,
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
	updated, err := session.NewStore(s.Service.Config.StateDir).AcknowledgeRules(id, globalWorkflowRevision, globalWorkflowDigest(), policy.Revision, digest)
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

func (s *Server) validateSessionRules(ctx context.Context, record session.Record, action string) error {
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
