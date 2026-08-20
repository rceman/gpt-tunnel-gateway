package mcp

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) boundTrainProject(ctx context.Context) (string, error) {
	sessionID := service.AgentSessionID(ctx)
	if sessionID == "" {
		return "", fmt.Errorf("Train action requires a bound session")
	}
	record, err := s.activeSession(sessionID)
	if err != nil {
		return "", err
	}
	if record.ProjectID == "" {
		return "", fmt.Errorf("Train action requires a bound project")
	}
	return record.ProjectID, nil
}
