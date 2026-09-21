package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

type SessionBootstrapResolution struct {
	Grant      sqlitestore.SessionBootstrapGrant
	SessionRef *string
}

func (s *Service) ensureOnboardSessionBootstrapGrant(ctx context.Context, projectID, projectCode string) (sqlitestore.SessionBootstrapGrant, error) {
	if s.Durability == nil || s.Durability.Local == nil {
		return sqlitestore.SessionBootstrapGrant{}, nil
	}
	return s.EnsureProjectSessionBootstrapGrant(ctx, projectID, projectCode)
}

func (s *Service) EnsureProjectSessionBootstrapGrant(ctx context.Context, projectID, projectCode string) (sqlitestore.SessionBootstrapGrant, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return sqlitestore.SessionBootstrapGrant{}, err
	}
	if err := model.ValidateProjectCode(projectCode); err != nil {
		return sqlitestore.SessionBootstrapGrant{}, err
	}
	if s.Durability == nil || s.Durability.Local == nil {
		return sqlitestore.SessionBootstrapGrant{}, fmt.Errorf("local session bootstrap grant store is unavailable")
	}
	return s.Durability.EnsureSessionBootstrapGrant(ctx, sqlitestore.SessionBootstrapGrant{
		ProjectID:   projectID,
		ProjectCode: projectCode,
		GatewayID:   s.Config.GatewayID,
		Role:        durableSession.RolePlanner,
	})
}

func (s *Service) SessionBootstrapToken(ctx context.Context, projectCode string) (string, error) {
	if err := model.ValidateProjectCode(projectCode); err != nil {
		return "", err
	}
	if s.Durability == nil || s.Durability.Local == nil {
		return "", fmt.Errorf("local session bootstrap grant store is unavailable")
	}
	ids, err := s.EffectiveProjectIDs()
	if err != nil {
		return "", fmt.Errorf("project registry unavailable: %w", err)
	}
	for _, projectID := range ids {
		project, readErr := s.EffectiveProjectConfig(projectID)
		if readErr != nil {
			continue
		}
		if project.ProjectCode != projectCode {
			continue
		}
		grant, readErr := s.Durability.ReadSessionBootstrapGrant(ctx, projectID)
		if readErr != nil || grant.ProjectCode != project.ProjectCode || grant.GatewayID != s.Config.GatewayID || grant.Role != durableSession.RolePlanner {
			return "", fmt.Errorf("project bootstrap token is unavailable")
		}
		return grant.Token, nil
	}
	return "", fmt.Errorf("unknown project code %q", projectCode)
}

func (s *Service) ResolveSessionBootstrapToken(ctx context.Context, token string) (SessionBootstrapResolution, error) {
	if s.Durability == nil || s.Durability.Local == nil {
		return SessionBootstrapResolution{}, fmt.Errorf("local session bootstrap grant store is unavailable")
	}
	grant, err := s.Durability.ReadSessionBootstrapGrantByToken(ctx, token)
	if err != nil {
		return SessionBootstrapResolution{}, fmt.Errorf("session bootstrap token is invalid")
	}
	if grant.GatewayID != s.Config.GatewayID {
		return SessionBootstrapResolution{}, fmt.Errorf("session bootstrap token is not valid for this Gateway")
	}
	if grant.Role != durableSession.RolePlanner {
		return SessionBootstrapResolution{}, fmt.Errorf("session bootstrap token is not a Planner grant")
	}
	project, projectErr := s.EffectiveProjectConfig(grant.ProjectID)
	if projectErr != nil || project.ProjectCode != grant.ProjectCode {
		return SessionBootstrapResolution{}, fmt.Errorf("session bootstrap token has no matching project")
	}
	workflowRole, ok := durableSession.WorkflowRoleByKey(grant.Role)
	if !ok {
		return SessionBootstrapResolution{}, fmt.Errorf("session bootstrap token has an unsupported role")
	}
	if err := model.ValidateProjectIdentifier(grant.ProjectID); err != nil {
		return SessionBootstrapResolution{}, fmt.Errorf("session bootstrap token has an invalid project")
	}
	if err := model.ValidateProjectCode(grant.ProjectCode); err != nil {
		return SessionBootstrapResolution{}, fmt.Errorf("session bootstrap token has an invalid project code")
	}
	if workflowRole.RefRequired {
		if strings.TrimSpace(grant.AgentID) == "" {
			return SessionBootstrapResolution{}, fmt.Errorf("session bootstrap token has no managed Agent")
		}
		binding, bindingErr := s.resolveSessionBootstrapAgent(grant.ProjectID, grant.AgentID)
		if bindingErr != nil {
			return SessionBootstrapResolution{}, bindingErr
		}
		ref := binding.SessionKey
		return SessionBootstrapResolution{
			Grant:      grant,
			SessionRef: &ref,
		}, nil
	}
	if grant.AgentID != "" {
		return SessionBootstrapResolution{}, fmt.Errorf("session bootstrap token has an invalid Agent")
	}
	return SessionBootstrapResolution{Grant: grant}, nil
}
