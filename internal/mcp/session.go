package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

type sessionContextKey struct{}

func withSession(ctx context.Context, record durableSession.Record) context.Context {
	return context.WithValue(ctx, sessionContextKey{}, record)
}

func (s *Server) activeSession(id string) (durableSession.Record, error) {
	record, err := durableSession.NewStoreWithDurability(s.Service.Durability).Get(id)
	if err != nil {
		return durableSession.Record{}, err
	}
	if record.Role == durableSession.RoleAdmin && s.Service.Config.GatewayID != "" && !strings.HasPrefix(record.ID, s.Service.Config.GatewayID+"_") {
		return durableSession.Record{}, fmt.Errorf("Admin Session belongs to another Gateway")
	}
	if record.Status != durableSession.StatusActive {
		return durableSession.Record{}, fmt.Errorf("session is not active")
	}
	return record, nil
}

func requireSessionRole(ctx context.Context, role string) error {
	return authority.RequireRole(ctx, role)
}

func withRoleAuthority(ctx context.Context, role string) (context.Context, error) {
	switch role {
	case durableSession.RolePlanner:
		return authority.WithPlanner(ctx), nil
	case durableSession.RoleLead:
		return authority.WithLead(ctx), nil
	case durableSession.RoleAdvisor:
		return authority.WithAdvisor(ctx), nil
	case durableSession.RoleWorker:
		return authority.WithWorker(ctx), nil
	default:
		return nil, fmt.Errorf("unsupported persisted session role %q", role)
	}
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
	return withRoleAuthority(bootstrapContext, role)
}

type sessionActionInput struct {
	Action      string  `json:"action"`
	SessionID   string  `json:"session_id"`
	ProjectID   string  `json:"project_id"`
	Role        string  `json:"role"`
	SessionType string  `json:"session_type"`
	Agent       *string `json:"agent"`
	Label       *string `json:"label"`
}

func publicSessionRecord(record durableSession.Record) map[string]any {
	result := normalizeObject(record)
	for _, key := range []string{"session_ref", "global_rules_revision", "global_rules_digest", "project_rules_digest"} {
		delete(result, key)
	}
	return result
}

func publicSessionResult(value any) any {
	switch result := value.(type) {
	case service.SessionResult:
		return map[string]any{"action": result.Action, "session": publicSessionRecord(result.Session)}
	case service.SessionListResult:
		sessions := make([]map[string]any, 0, len(result.Sessions))
		for _, item := range result.Sessions {
			sessions = append(sessions, map[string]any{"session_id": item.SessionID, "role": item.Role, "project_id": item.ProjectID})
		}
		return map[string]any{"action": result.Action, "sessions": sessions}
	default:
		return value
	}
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
		workflowRole, ok := durableSession.WorkflowRoleByKey(input.Role)
		if !ok {
			return nil, fmt.Errorf("unsupported session role %q", input.Role)
		}
		var sessionRef *string
		if workflowRole.RefRequired {
			if input.Agent == nil || *input.Agent == "" {
				return nil, fmt.Errorf("managed role logical Agent is required")
			}
			_, binding, err := s.resolveHostLocalAgentBinding(input.ProjectID, *input.Agent)
			if err != nil {
				return nil, err
			}
			ref := binding.SessionKey
			sessionRef = &ref
		} else if input.Agent != nil {
			return nil, fmt.Errorf("logical Agent is only valid for managed workflow roles")
		}
		bootstrapContext, err := authority.BootstrapSessionAuthority(ctx)
		if err != nil {
			return nil, err
		}
		result, err := s.Service.SessionStart(bootstrapContext, service.SessionStartInput{ProjectID: input.ProjectID, Role: input.Role, SessionType: input.SessionType, SessionRef: sessionRef, Label: input.Label})
		if err != nil {
			return nil, err
		}
		return publicSessionResult(result), nil
	case "list":
		result, err := s.Service.SessionList()
		if err != nil {
			return nil, err
		}
		return publicSessionResult(result), nil
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
		return publicSessionResult(result), nil
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
		var sessionRef *string
		workflowRole, ok := durableSession.WorkflowRoleByKey(info.Session.Role)
		if !ok {
			return nil, fmt.Errorf("unsupported persisted session role %q", info.Session.Role)
		}
		if input.Agent != nil {
			if !workflowRole.RefRequired || *input.Agent == "" {
				return nil, fmt.Errorf("logical Agent is only valid for a non-empty managed workflow role")
			}
			_, binding, err := s.resolveHostLocalAgentBinding(info.Session.ProjectID, *input.Agent)
			if err != nil {
				return nil, err
			}
			ref := binding.SessionKey
			sessionRef = &ref
		}
		result, err := s.Service.SessionUpdate(roleContext, service.SessionUpdateInput{SessionID: input.SessionID, SessionRef: sessionRef, Label: input.Label})
		if err != nil {
			return nil, err
		}
		return publicSessionResult(result), nil
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
		result, err := s.Service.SessionEnd(roleContext, input.SessionID)
		if err != nil {
			return nil, err
		}
		return publicSessionResult(result), nil
	default:
		return nil, fmt.Errorf("unknown session action %q", input.Action)
	}
}
