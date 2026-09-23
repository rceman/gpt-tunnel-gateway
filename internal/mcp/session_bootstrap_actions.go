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
	token := str("Per-project Planner bootstrap grant returned by the authenticated gpt-tunnel project token operator command.")
	token["minLength"] = 24
	token["maxLength"] = 256
	return obj(map[string]any{"token": token}, "token")
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
		"rules":   closedOutput(map[string]any{"items": outputArray(rule)}, "items"),
	}, "session", "gateway", "project", "role", "rules")
}

func (s *Server) sessionStartPublic(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		Token string `json:"token"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	resolution, err := s.Service.ResolveSessionBootstrapToken(ctx, in.Token)
	if err != nil {
		return nil, err
	}
	grant := resolution.Grant
	bootstrapContext, err := authority.BootstrapSessionAuthority(ctx)
	if err != nil {
		return nil, err
	}
	sessionContext, err := withRoleAuthority(bootstrapContext, grant.Role)
	if err != nil {
		return nil, err
	}
	started, err := s.Service.SessionStart(sessionContext, service.SessionStartInput{
		ProjectID:   grant.ProjectID,
		ProjectCode: grant.ProjectCode,
		Role:        grant.Role,
		SessionType: durableSession.SessionTypeChatGPT,
		SessionRef:  resolution.SessionRef,
	})
	if err != nil {
		return nil, err
	}
	project, err := s.Service.EffectiveProjectConfig(grant.ProjectID)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"session": started.Session.ID,
		"gateway": map[string]any{"key": grant.GatewayID},
		"project": map[string]any{"key": project.ProjectCode, "name": grant.ProjectID},
		"role":    started.Session.Role,
		"rules":   publicSessionRules(),
	}
	if grant.AgentID != "" {
		result["agent"] = grant.AgentID
	}
	return result, nil
}

func publicSessionRules() map[string]any {
	content, _ := globalWorkflowRules()["content"].(string)
	return map[string]any{
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
