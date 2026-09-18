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
	agent := str("Logical Agent key used for server-side managed-role binding.")
	agent["minLength"] = 1
	agent["maxLength"] = 128
	gateway := str("Canonical registered Gateway key.")
	gateway["pattern"] = `^[A-Z]{3}$`
	project := str("Canonical registered project code.")
	project["pattern"] = `^[A-Z]{3}$`
	role := durableSession.WorkflowRoleSchema("Server-authorized durable session role.")
	label := str("Optional bounded durable Session label.")
	label["maxLength"] = 256
	schema := obj(map[string]any{
		"agent":   agent,
		"gateway": gateway,
		"label":   label,
		"project": project,
		"role":    role,
	}, "project", "role")
	schema["if"] = map[string]any{
		"properties": map[string]any{"role": map[string]any{"enum": []any{durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker}}},
		"required":   []string{"role"},
	}
	schema["then"] = map[string]any{"required": []string{"agent"}}
	return schema
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
		"agent":   outputString(),
		"label":   outputString(),
		"session": sessionIDOutputSchema(),
		"gateway": gateway,
		"project": project,
		"role":    durableSession.WorkflowRoleOutputSchema(),
		"rules": closedOutput(map[string]any{
			"digest": outputString(),
			"items":  outputArray(rule),
		}, "digest", "items"),
	}, "session", "gateway", "project", "role", "rules")
}

func (s *Server) sessionStartPublic(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		Agent   string  `json:"agent"`
		Gateway string  `json:"gateway"`
		Label   *string `json:"label"`
		Project string  `json:"project"`
		Role    string  `json:"role"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if in.Project == "" || in.Role == "" {
		return nil, fmt.Errorf("project and role are required")
	}
	resolvedGateway, err := s.resolvePublicGateway(in.Gateway)
	if err != nil {
		return nil, err
	}
	in.Gateway = resolvedGateway
	resolution, err := s.Service.EffectiveProjectSnapshot()
	if err != nil {
		return nil, fmt.Errorf("project registry unavailable: %w", err)
	}
	projectID, project, err := resolvePublicProject(resolution, in.Project)
	if err != nil {
		return nil, err
	}
	workflowRole, ok := durableSession.WorkflowRoleByKey(in.Role)
	if !ok {
		return nil, fmt.Errorf("unsupported session role %q", in.Role)
	}
	var sessionRef *string
	if workflowRole.RefRequired {
		if in.Agent == "" {
			return nil, fmt.Errorf("managed role logical Agent is required")
		}
		bindingAgent, binding, bindingErr := s.resolveHostLocalAgentBinding(projectID, in.Agent)
		if bindingErr != nil {
			return nil, bindingErr
		}
		if bindingAgent != in.Agent {
			return nil, fmt.Errorf("managed role logical Agent binding mismatch")
		}
		ref := binding.SessionKey
		sessionRef = &ref
	} else if in.Agent != "" {
		return nil, fmt.Errorf("logical Agent is only valid for managed workflow roles")
	}
	bootstrapContext, err := authority.BootstrapSessionAuthority(ctx)
	if err != nil {
		return nil, err
	}
	sessionContext, err := withRoleAuthority(bootstrapContext, in.Role)
	if err != nil {
		return nil, err
	}
	started, err := s.Service.SessionStart(sessionContext, service.SessionStartInput{
		ProjectID:   projectID,
		ProjectCode: project.ProjectCode,
		Role:        in.Role,
		SessionType: durableSession.SessionTypeChatGPT,
		SessionRef:  sessionRef,
		Label:       in.Label,
	})
	if err != nil {
		return nil, err
	}
	rules := publicSessionRules()
	result := map[string]any{
		"session": started.Session.ID,
		"gateway": map[string]any{"key": in.Gateway},
		"project": map[string]any{"key": project.ProjectCode, "name": projectID},
		"role":    started.Session.Role,
		"rules":   rules,
	}
	if in.Agent != "" {
		result["agent"] = in.Agent
	}
	if started.Session.Label != nil {
		result["label"] = *started.Session.Label
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

func (s *Server) validateSessionRules(ctx context.Context, record durableSession.Record, action string) error {
	if record.ProjectID == "" || record.GlobalRulesRevision == "" || action == "rule/effective" || action == "session/info" || action == "session/end" || action == "project/list" {
		return nil
	}
	if record.GlobalRulesRevision != globalWorkflowRevision || record.GlobalRulesDigest != globalWorkflowDigest() {
		return fmt.Errorf("RULES_REFRESH_REQUIRED: global workflow rules changed")
	}
	digest, err := s.Service.ProjectRuleEffectiveDigest(ctx, record.ProjectID)
	if err != nil {
		return fmt.Errorf("RULES_REFRESH_REQUIRED: project rules unavailable")
	}
	if record.ProjectRulesDigest != digest {
		return fmt.Errorf("RULES_REFRESH_REQUIRED: read current project rules")
	}
	return nil
}
