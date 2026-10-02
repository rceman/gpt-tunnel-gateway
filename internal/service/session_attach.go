package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/workflowrole"
)

// SessionAttachInput selects the exact managed role Session attachment for one
// already registered same-project Agent. Project and Gateway identity are
// server-derived; callers name only the project code, role, and Agent.
type SessionAttachInput struct {
	ProjectCode string  `json:"project_code"`
	Role        string  `json:"role"`
	AgentID     string  `json:"agent_id"`
	Label       *string `json:"label,omitempty"`
}

// SessionAttachResult carries the created or reused durable Session plus the
// role/Agent/runtime facts a local managed runtime needs.
type SessionAttachResult struct {
	Action      string                `json:"action"`
	Status      string                `json:"status"`
	ProjectID   string                `json:"project_id"`
	ProjectCode string                `json:"project_code"`
	Role        string                `json:"role"`
	AgentID     string                `json:"agent_id"`
	SessionRef  string                `json:"session_ref"`
	Session     durableSession.Record `json:"session"`
}

// SessionAttach creates or reuses one durable managed-role Session bound to the
// registered Agent's configured runtime. Local same-user operator authority
// replaces the session-scoped role check; Planner remains token-bootstrapped
// through session/start and is rejected here. The exact active attachment is
// idempotent; a conflicting active role Session fails closed.
func (s *Service) SessionAttach(ctx context.Context, in SessionAttachInput) (SessionAttachResult, error) {
	if err := model.ValidateProjectCode(in.ProjectCode); err != nil {
		return SessionAttachResult{}, err
	}
	role := strings.ToLower(strings.TrimSpace(in.Role))
	if role == workflowrole.RolePlanner {
		return SessionAttachResult{}, fmt.Errorf("Planner Sessions are bootstrapped only through the project token and session/start")
	}
	if !workflowrole.Is(role) || !workflowrole.RequiresRuntime(role) {
		return SessionAttachResult{}, fmt.Errorf("unsupported managed Session role %q", in.Role)
	}
	agentID := strings.TrimSpace(in.AgentID)
	if err := model.ValidateObjectIdentifier(agentID); err != nil {
		return SessionAttachResult{}, fmt.Errorf("Agent reference is required: %w", err)
	}
	projectID, projectCode, err := s.projectIDForCode(ctx, in.ProjectCode)
	if err != nil {
		return SessionAttachResult{}, err
	}
	if _, err := s.projectConfigurationRead(ctx, projectID, true); err != nil {
		return SessionAttachResult{}, fmt.Errorf("session project Shared configuration or retirement state is unavailable: %w", err)
	}
	agent, err := s.AgentRead(ctx, projectID, agentID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SessionAttachResult{}, fmt.Errorf("Agent %q is not registered for project %q", agentID, projectID)
		}
		return SessionAttachResult{}, err
	}
	if agent.ProjectID != projectID || agent.Role != model.AgentRoleCoding || !agent.Enabled {
		return SessionAttachResult{}, fmt.Errorf("Agent %q is not an enabled registered Agent for project %q", agentID, projectID)
	}
	if agent.WorkflowRole != role {
		return SessionAttachResult{}, fmt.Errorf("Agent %q workflow role %q conflicts with requested role %q", agentID, agent.WorkflowRole, role)
	}
	binding, bound := s.agentBinding(projectID, agentID)
	if !bound || binding.Validate() != nil || binding.SessionKey == "" {
		return SessionAttachResult{}, fmt.Errorf("Agent %q has no managed runtime binding for project %q", agentID, projectID)
	}
	sessionRef := binding.SessionKey
	if s.Durability == nil || s.Durability.Local == nil {
		return SessionAttachResult{}, fmt.Errorf("local session store is unavailable")
	}
	store := durableSession.NewStoreWithGateway(s.Durability, s.Config.GatewayID)
	records, err := durableSession.NewStoreWithDurability(s.Durability).List()
	if err != nil {
		return SessionAttachResult{}, fmt.Errorf("durable Session authority is unavailable: %w", err)
	}
	active := make([]durableSession.Record, 0, 1)
	for _, record := range records {
		if record.Status == durableSession.StatusActive && record.ProjectID == projectID && record.Role == role {
			active = append(active, record)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].ID < active[j].ID })
	result := SessionAttachResult{
		Action:      "attach",
		ProjectID:   projectID,
		ProjectCode: projectCode,
		Role:        role,
		AgentID:     agentID,
		SessionRef:  sessionRef,
	}
	switch {
	case len(active) == 1 && active[0].SessionRef != nil && *active[0].SessionRef == sessionRef:
		result.Status = "already_attached"
		result.Session = active[0]
		return result, nil
	case len(active) > 0:
		return SessionAttachResult{}, fmt.Errorf("active %s Session for project %q is bound to a different managed runtime; end it through session end before re-attaching", role, projectID)
	}
	record, err := store.Create(durableSession.CreateInput{ProjectID: projectID, ProjectCode: projectCode, Role: role, SessionType: durableSession.SessionTypeChatGPT, SessionRef: &sessionRef, Label: in.Label})
	if err != nil {
		return SessionAttachResult{}, err
	}
	result.Status = "attached"
	result.Session = record
	return result, nil
}

// projectIDForCode resolves an exact project code to the one configured Local
// project that claims it in canonical Hub identifiers.
func (s *Service) projectIDForCode(ctx context.Context, code string) (string, string, error) {
	ids, err := s.EffectiveProjectIDs()
	if err != nil {
		return "", "", err
	}
	matched := ""
	matchedCode := ""
	for _, projectID := range ids {
		identifiers, readErr := s.ProjectIdentifiersRead(ctx, projectID)
		if readErr != nil || identifiers.ProjectCode != code {
			continue
		}
		if matched != "" {
			return "", "", fmt.Errorf("project code %q is claimed by multiple configured projects", code)
		}
		matched, matchedCode = projectID, identifiers.ProjectCode
	}
	if matched == "" {
		return "", "", fmt.Errorf("project code %q is not a configured Local project", code)
	}
	return matched, matchedCode, nil
}
