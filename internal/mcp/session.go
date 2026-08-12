package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

type sessionContextKey struct{}

func withSession(ctx context.Context, record durableSession.Record) context.Context {
	return context.WithValue(ctx, sessionContextKey{}, record)
}

func (s *Server) activeSession(id string) (durableSession.Record, error) {
	record, err := durableSession.NewStore(s.Service.Config.StateDir).Get(id)
	if err != nil {
		return durableSession.Record{}, err
	}
	if record.Status != durableSession.StatusActive {
		return durableSession.Record{}, fmt.Errorf("session is not active")
	}
	return record, nil
}

func requireSessionRole(ctx context.Context, role string) error {
	return authority.RequireRole(ctx, role)
}

// existingSessionRoleContext converts trusted bootstrap authority into the
// exact durable role recorded in the session. The combined marker is accepted
// only as the creation capability; it is never used as persisted session
// authority.
func existingSessionRoleContext(ctx context.Context, role string) (context.Context, error) {
	bootstrapContext := ctx
	if elevated, err := authority.BootstrapSessionAuthority(ctx); err == nil {
		bootstrapContext = elevated
	}
	if err := authority.RequireRole(bootstrapContext, role); err != nil {
		return nil, err
	}
	switch role {
	case durableSession.RolePlanner:
		return authority.WithPlanner(bootstrapContext), nil
	case durableSession.RoleDelivery:
		return authority.WithDelivery(bootstrapContext), nil
	default:
		return nil, fmt.Errorf("unsupported persisted session role %q", role)
	}
}

type sessionActionInput struct {
	Action      string  `json:"action"`
	SessionID   string  `json:"session_id"`
	ProjectID   string  `json:"project_id"`
	Role        string  `json:"role"`
	SessionType string  `json:"session_type"`
	SessionRef  *string `json:"session_ref"`
	Label       *string `json:"label"`
}

func (s *Server) sessionAction(ctx context.Context, raw json.RawMessage) (any, error) {
	var input sessionActionInput
	if err := decode(raw, &input); err != nil {
		return nil, err
	}
	switch input.Action {
	case "start":
		if input.SessionID != "" {
			return nil, fmt.Errorf("session_id is not accepted by session.start")
		}
		bootstrapContext, err := authority.BootstrapSessionAuthority(ctx)
		if err != nil {
			return nil, err
		}
		result, err := s.Service.SessionStart(bootstrapContext, service.SessionStartInput{ProjectID: input.ProjectID, Role: input.Role, SessionType: input.SessionType, SessionRef: input.SessionRef, Label: input.Label})
		if err != nil {
			return nil, err
		}
		return result, nil
	case "info":
		if input.SessionID == "" {
			return nil, fmt.Errorf("session_id is required")
		}
		result, err := s.Service.SessionInfo(ctx, input.SessionID)
		if err != nil {
			return nil, err
		}
		if _, err := existingSessionRoleContext(ctx, result.Session.Role); err != nil {
			return nil, err
		}
		return result, nil
	case "update":
		if input.SessionID == "" {
			return nil, fmt.Errorf("session_id is required")
		}
		info, err := s.Service.SessionInfo(ctx, input.SessionID)
		if err != nil {
			return nil, err
		}
		roleContext, err := existingSessionRoleContext(ctx, info.Session.Role)
		if err != nil {
			return nil, err
		}
		return s.Service.SessionUpdate(roleContext, service.SessionUpdateInput{SessionID: input.SessionID, SessionRef: input.SessionRef, Label: input.Label})
	case "end":
		if input.SessionID == "" {
			return nil, fmt.Errorf("session_id is required")
		}
		info, err := s.Service.SessionInfo(ctx, input.SessionID)
		if err != nil {
			return nil, err
		}
		roleContext, err := existingSessionRoleContext(ctx, info.Session.Role)
		if err != nil {
			return nil, err
		}
		return s.Service.SessionEnd(roleContext, input.SessionID)
	default:
		return nil, fmt.Errorf("unknown session action %q", input.Action)
	}
}
