package service

import (
	"context"
	"fmt"
	"strings"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func (s *Service) validateTaskExecutionAgentSession(ctx context.Context, projectID string, resolved ResolvedAgent, sessionID string) error {
	if !strings.HasPrefix(sessionID, durableSession.SessionIDPrefixAgent+"-") {
		return fmt.Errorf("Task requires an active Agent session")
	}
	record, err := durableSession.NewStoreWithDurability(s.Durability).Get(sessionID)
	if err != nil {
		return fmt.Errorf("Task Agent session is unavailable: %w", err)
	}
	if record.ProjectID != projectID || record.Role != durableSession.RoleAgent || record.Status != durableSession.StatusActive || record.SessionRef == nil || *record.SessionRef != resolved.SessionKey {
		return fmt.Errorf("Task is assigned to a different Agent session")
	}
	return nil
}
