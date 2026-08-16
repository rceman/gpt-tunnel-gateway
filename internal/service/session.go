package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

type SessionStartInput struct {
	ProjectID   string
	ProjectCode string
	Role        string
	SessionType string
	SessionRef  *string
	Label       *string
}

type SessionBindInput struct {
	SessionID  string
	ProjectID  string
	SessionRef *string
}

type SessionUpdateInput struct {
	SessionID  string
	SessionRef *string
	Label      *string
}

type SessionResult struct {
	Action  string                `json:"action"`
	Session durableSession.Record `json:"session"`
}

type SessionListItem struct {
	SessionID string  `json:"session_id"`
	Role      string  `json:"role"`
	ProjectID string  `json:"project_id"`
	Ref       *string `json:"ref"`
}

type SessionListResult struct {
	Action   string            `json:"action"`
	Sessions []SessionListItem `json:"sessions"`
}

func (s *Service) SessionStart(ctx context.Context, input SessionStartInput) (SessionResult, error) {
	if err := authority.RequireRole(ctx, input.Role); err != nil {
		return SessionResult{}, err
	}
	project, err := s.ProjectRead(ctx, input.ProjectID)
	if err != nil {
		return SessionResult{}, fmt.Errorf("session project is not durably registered: %w", err)
	}
	if err := model.ValidateProject(project); err != nil {
		return SessionResult{}, fmt.Errorf("session project is invalid: %w", err)
	}
	if project.ID != input.ProjectID || project.Status != "active" {
		return SessionResult{}, fmt.Errorf("session project is not active")
	}
	identifiers, identifiersErr := s.ProjectIdentifiersRead(ctx, input.ProjectID)
	if identifiersErr != nil {
		return SessionResult{}, fmt.Errorf("session project identifiers unavailable: %w", identifiersErr)
	}
	projectCode := identifiers.ProjectCode
	if input.ProjectCode != "" && input.ProjectCode != projectCode {
		return SessionResult{}, fmt.Errorf("session project code %q does not match durable project code %q", input.ProjectCode, projectCode)
	}
	if _, err := s.EffectiveProjectConfig(input.ProjectID); err != nil {
		return SessionResult{}, err
	}
	record, err := durableSession.NewStore(s.Config.StateDir).Create(durableSession.CreateInput{ProjectID: input.ProjectID, ProjectCode: projectCode, Role: input.Role, SessionType: input.SessionType, SessionRef: input.SessionRef, Label: input.Label})
	if err != nil {
		return SessionResult{}, err
	}
	return SessionResult{
		Action:  "start",
		Session: record,
	}, nil
}

func (s *Service) SessionStartByCode(ctx context.Context, projectCode, role string, sessionRef, label *string) (SessionResult, error) {
	if err := model.ValidateProjectCode(projectCode); err != nil {
		return SessionResult{}, err
	}
	ids, err := s.EffectiveProjectIDs()
	if err != nil {
		return SessionResult{}, err
	}
	for _, projectID := range ids {
		identifiers, readErr := s.ProjectIdentifiersRead(ctx, projectID)
		if readErr != nil {
			continue
		}
		if identifiers.ProjectCode == projectCode {
			return s.SessionStart(ctx, SessionStartInput{
				ProjectID:   projectID,
				ProjectCode: projectCode,
				Role:        role,
				SessionType: durableSession.SessionTypeChatGPT,
				SessionRef:  sessionRef,
				Label:       label,
			})
		}
	}
	return SessionResult{}, fmt.Errorf("unknown project code %q", projectCode)
}

func (s *Service) SessionStartUnbound(ctx context.Context, role string, label *string) (SessionResult, error) {
	return SessionResult{}, fmt.Errorf("unbound sessions are not supported")
}

func (s *Service) SessionBind(ctx context.Context, input SessionBindInput) (SessionResult, error) {
	if err := authority.RequireRole(ctx, durableSession.RolePlanner); err != nil {
		if err := authority.RequireRole(ctx, durableSession.RoleDelivery); err != nil {
			return SessionResult{}, err
		}
	}
	project, err := s.ProjectRead(ctx, input.ProjectID)
	if err != nil {
		return SessionResult{}, fmt.Errorf("session project is not durably registered: %w", err)
	}
	if err := model.ValidateProject(project); err != nil {
		return SessionResult{}, fmt.Errorf("session project is invalid: %w", err)
	}
	if project.Status != "active" {
		return SessionResult{}, fmt.Errorf("session project is not active")
	}
	record, err := durableSession.NewStore(s.Config.StateDir).Bind(input.SessionID, input.ProjectID)
	if err != nil {
		return SessionResult{}, err
	}
	if input.SessionRef != nil {
		record, err = durableSession.NewStore(s.Config.StateDir).Update(record.ID, durableSession.UpdateInput{SessionRef: input.SessionRef})
		if err != nil {
			return SessionResult{}, err
		}
	}
	return SessionResult{
		Action:  "bind",
		Session: record,
	}, nil
}

func (s *Service) SessionInfo(ctx context.Context, sessionID string) (SessionResult, error) {
	record, err := durableSession.NewStore(s.Config.StateDir).Get(sessionID)
	if err != nil {
		return SessionResult{}, err
	}
	return SessionResult{
		Action:  "info",
		Session: record,
	}, nil
}

func (s *Service) SessionList() (SessionListResult, error) {
	records, err := durableSession.NewStore(s.Config.StateDir).List()
	if err != nil {
		return SessionListResult{}, err
	}
	items := make([]SessionListItem, 0, len(records))
	for _, record := range records {
		if record.Status != durableSession.StatusActive {
			continue
		}
		ref := cloneSessionString(record.SessionRef)
		items = append(items, SessionListItem{
			SessionID: record.ID,
			Role:      record.Role,
			ProjectID: record.ProjectID,
			Ref:       ref,
		})
	}
	return SessionListResult{
		Action:   "list",
		Sessions: items,
	}, nil
}

func cloneSessionString(value *string) *string {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func (s *Service) SessionUpdate(ctx context.Context, input SessionUpdateInput) (SessionResult, error) {
	record, err := durableSession.NewStore(s.Config.StateDir).Update(input.SessionID, durableSession.UpdateInput{SessionRef: input.SessionRef, Label: input.Label})
	if err != nil {
		return SessionResult{}, err
	}
	return SessionResult{
		Action:  "update",
		Session: record,
	}, nil
}

func (s *Service) SessionEnd(ctx context.Context, sessionID string) (SessionResult, error) {
	record, err := durableSession.NewStore(s.Config.StateDir).End(sessionID)
	if err != nil {
		return SessionResult{}, err
	}
	return SessionResult{
		Action:  "end",
		Session: record,
	}, nil
}
