package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
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
	agentDir := s.projectPrefix(config.GTWProjectID) + "/agents"
	legacyPath := agentDir + "/" + config.LegacyGTWWorkerAgentID + ".json"
	canonicalPath := agentDir + "/" + config.GTWWorkerAgentID + ".json"
	paths, err := s.Hub.List(ctx, agentDir, ".json")
	if err != nil {
		return fmt.Errorf("list GTW Worker Agent records: %w", err)
	}
	legacyPresent := containsPath(paths, legacyPath)
	canonicalPresent := containsPath(paths, canonicalPath)
	if legacyPresent && canonicalPresent {
		return fmt.Errorf("GTW Worker Agent identity collision: both %q and %q exist", config.LegacyGTWWorkerAgentID, config.GTWWorkerAgentID)
	}
	if !legacyPresent {
		if canonicalPresent {
			var canonical model.Agent
			if err := s.Hub.ReadJSON(ctx, canonicalPath, &canonical); err != nil {
				return fmt.Errorf("read canonical GTW Worker Agent: %w", err)
			}
			return validateGTWWorkerAgent(canonical, config.GTWWorkerAgentID)
		}
		return nil
	}
	var legacy model.Agent
	if err := s.Hub.ReadJSON(ctx, legacyPath, &legacy); err != nil {
		return fmt.Errorf("read legacy GTW Worker Agent: %w", err)
	}
	if err := validateGTWWorkerAgent(legacy, config.LegacyGTWWorkerAgentID); err != nil {
		return err
	}
	expected, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		return fmt.Errorf("read Hub revision for GTW Worker Agent migration: %w", err)
	}
	if _, err := s.Hub.Transact(ctx, expected, "gateway: rename GTW Worker managed Agent", func(worktree string) ([]string, error) {
		var legacy model.Agent
		if err := readWorktreeJSON(worktree, legacyPath, &legacy); err != nil {
			return nil, err
		}
		if err := validateGTWWorkerAgent(legacy, config.LegacyGTWWorkerAgentID); err != nil {
			return nil, err
		}
		if _, err := os.Stat(filepath.Join(worktree, filepath.FromSlash(canonicalPath))); err == nil {
			return nil, fmt.Errorf("GTW Worker Agent identity collision during commit")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		legacy.AgentID = config.GTWWorkerAgentID
		if err := model.ValidateAgent(legacy); err != nil {
			return nil, fmt.Errorf("validate canonical GTW Worker Agent: %w", err)
		}
		if err := hub.WriteJSON(worktree, canonicalPath, legacy); err != nil {
			return nil, err
		}
		if err := os.Remove(filepath.Join(worktree, filepath.FromSlash(legacyPath))); err != nil {
			return nil, err
		}
		return []string{legacyPath, canonicalPath}, nil
	}); err != nil {
		return fmt.Errorf("commit GTW Worker Agent identity migration: %w", err)
	}
	return nil
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
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
