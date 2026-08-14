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
	Role        string
	SessionType string
	SessionRef  *string
	Label       *string
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
	if _, err := s.EffectiveProjectConfig(input.ProjectID); err != nil {
		return SessionResult{}, err
	}
	record, err := durableSession.NewStore(s.Config.StateDir).Create(durableSession.CreateInput{ProjectID: input.ProjectID, Role: input.Role, SessionType: input.SessionType, SessionRef: input.SessionRef, Label: input.Label})
	if err != nil {
		return SessionResult{}, err
	}
	return SessionResult{
		Action:  "start",
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
