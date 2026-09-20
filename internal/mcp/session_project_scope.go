package mcp

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) requireSessionProjectScope(ctx context.Context, targetID string) error {
	currentID := service.AgentSessionID(ctx)
	if currentID == "" {
		return nil
	}
	current, err := s.activeSession(currentID)
	if err != nil {
		return fmt.Errorf("session discovery authority is unavailable: %w", err)
	}
	target, err := s.activeSession(targetID)
	if err != nil {
		return err
	}
	if current.ProjectID == "" || target.ProjectID == "" || current.ProjectID != target.ProjectID {
		return fmt.Errorf("session is outside the authenticated project scope")
	}
	return nil
}
