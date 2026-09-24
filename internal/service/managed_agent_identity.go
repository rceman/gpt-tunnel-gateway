package service

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) MigrateGTWWorkerIdentityLocalShared(ctx context.Context) error {
	if _, err := s.EffectiveProjectConfig(config.GTWProjectID); err != nil || s.Durability == nil {
		return nil
	}
	if err := s.Durability.MigrateLocalAgentIdentity(ctx, config.GTWProjectID, config.LegacyGTWWorkerAgentID, config.GTWWorkerAgentID); err != nil {
		return fmt.Errorf("migrate GTW Worker Local Agent projection: %w", err)
	}
	if err := s.Durability.MigrateTaskExecutionAgentIdentity(ctx, config.GTWProjectID, config.LegacyGTWWorkerAgentID, config.GTWWorkerAgentID); err != nil {
		return fmt.Errorf("migrate GTW Worker nonterminal execution identity: %w", err)
	}
	return nil
}

func (s *Service) ReconcileGTWWorkerIdentity(ctx context.Context) error {
	if _, err := s.EffectiveProjectConfig(config.GTWProjectID); err != nil {
		return nil
	}
	if s.Durability == nil {
		return fmt.Errorf("Local durability is required for GTW Worker identity")
	}
	agent, err := s.AgentRead(ctx, config.GTWProjectID, config.GTWWorkerAgentID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read canonical Local GTW Worker Agent: %w", err)
	}
	return validateGTWWorkerAgent(agent, config.GTWWorkerAgentID)
}

func validateGTWWorkerAgent(agent model.Agent, wantID string) error {
	if err := model.ValidateAgent(agent); err != nil {
		return fmt.Errorf("validate GTW Worker Agent %q: %w", wantID, err)
	}
	if agent.ProjectID != config.GTWProjectID || agent.AgentID != wantID {
		return fmt.Errorf("GTW Worker Agent %q identity mismatch", wantID)
	}
	return nil
}
