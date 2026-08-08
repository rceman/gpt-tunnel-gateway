package service

import (
	"context"

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

func (s *Service) SessionStart(ctx context.Context, input SessionStartInput) (SessionResult, error) {
	if _, err := s.EffectiveProjectConfig(input.ProjectID); err != nil {
		return SessionResult{}, err
	}
	record, err := durableSession.NewStore(s.Config.StateDir).Create(durableSession.CreateInput{ProjectID: input.ProjectID, Role: input.Role, SessionType: input.SessionType, SessionRef: input.SessionRef, Label: input.Label})
	if err != nil {
		return SessionResult{}, err
	}
	return SessionResult{Action: "start", Session: record}, nil
}

func (s *Service) SessionInfo(ctx context.Context, sessionID string) (SessionResult, error) {
	record, err := durableSession.NewStore(s.Config.StateDir).Get(sessionID)
	if err != nil {
		return SessionResult{}, err
	}
	return SessionResult{Action: "info", Session: record}, nil
}

func (s *Service) SessionUpdate(ctx context.Context, input SessionUpdateInput) (SessionResult, error) {
	record, err := durableSession.NewStore(s.Config.StateDir).Update(input.SessionID, durableSession.UpdateInput{SessionRef: input.SessionRef, Label: input.Label})
	if err != nil {
		return SessionResult{}, err
	}
	return SessionResult{Action: "update", Session: record}, nil
}

func (s *Service) SessionEnd(ctx context.Context, sessionID string) (SessionResult, error) {
	record, err := durableSession.NewStore(s.Config.StateDir).End(sessionID)
	if err != nil {
		return SessionResult{}, err
	}
	return SessionResult{Action: "end", Session: record}, nil
}
