package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

const (
	defaultRegisteredAgentReasoning = model.ReasoningHigh
)

var defaultRegisteredAgentCapabilities = []string{"git", "review"}

func validatePortableAgentWorkflowRole(role string) error {
	if role == "" {
		return nil
	}
	if _, ok := durableSession.WorkflowRoleByKey(role); !ok {
		return fmt.Errorf("unsupported Agent workflow role %q", role)
	}
	return nil
}

// AgentRegister creates one portable project Agent and its Local projection.
// It is deliberately separate from AgentUpdate: bootstrap must never turn an
// update of a missing record into an implicit registration.
func (s *Service) AgentRegister(ctx context.Context, in AgentRegisterInput) (model.Agent, OperationResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	if err := model.ValidateObjectIdentifier(in.AgentID); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	if err := validatePortableAgentWorkflowRole(in.WorkflowRole); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	if s.Durability == nil {
		return model.Agent{}, OperationResult{}, fmt.Errorf("Shared/Local durability is required for Agent registration")
	}
	if _, err := s.EffectiveProjectConfig(in.ProjectID); err != nil {
		return model.Agent{}, OperationResult{}, err
	}

	snapshot, err := s.Hub.ReadSnapshot(ctx)
	if err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	projectCtx := hub.WithReadSnapshot(ctx, snapshot)
	if _, err := s.ProjectRead(projectCtx, in.ProjectID); err != nil {
		_ = snapshot.Close()
		return model.Agent{}, OperationResult{}, err
	}
	paths, err := snapshot.List(projectCtx, s.projectPrefix(in.ProjectID)+"/agents", ".json")
	if err != nil {
		_ = snapshot.Close()
		return model.Agent{}, OperationResult{}, err
	}
	for _, path := range paths {
		var existing model.Agent
		if err := snapshot.ReadJSON(projectCtx, path, &existing); err != nil {
			_ = snapshot.Close()
			return model.Agent{}, OperationResult{}, err
		}
		if err := model.ValidateAgent(existing); err != nil || existing.ProjectID != in.ProjectID {
			_ = snapshot.Close()
			return model.Agent{}, OperationResult{}, fmt.Errorf("invalid existing Agent record %q", path)
		}
		if existing.AgentID == in.AgentID {
			_ = snapshot.Close()
			return model.Agent{}, OperationResult{}, fmt.Errorf("Agent %q is already registered", in.AgentID)
		}
	}
	hubRevision := snapshot.Revision()
	if in.ExpectedHubRevision != "" && in.ExpectedHubRevision != hubRevision {
		_ = snapshot.Close()
		return model.Agent{}, OperationResult{}, fmt.Errorf("Hub revision mismatch: expected %s, got %s", in.ExpectedHubRevision, hubRevision)
	}
	if err := snapshot.Close(); err != nil {
		return model.Agent{}, OperationResult{}, fmt.Errorf("close Hub Agent registration preflight: %w", err)
	}

	now := time.Now().UTC()
	agent := model.Agent{
		SchemaVersion:        model.AgentSchemaVersion,
		ProjectID:            in.ProjectID,
		AgentID:              in.AgentID,
		Role:                 model.AgentRoleCoding,
		WorkflowRole:         in.WorkflowRole,
		Enabled:              true,
		RecommendedReasoning: defaultRegisteredAgentReasoning,
		Capabilities:         append([]string(nil), defaultRegisteredAgentCapabilities...),
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if err := s.validateManagedRuntimeBindingCollision(ctx, agent); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	path := s.agentPath(in.ProjectID, in.AgentID)
	tx, err := s.Hub.Transact(ctx, hubRevision, "gateway: register Agent "+in.ProjectID+"/"+in.AgentID, func(worktree string) ([]string, error) {
		if _, err := os.Stat(filepath.Join(worktree, filepath.FromSlash(path))); err == nil {
			return nil, fmt.Errorf("Agent %q is already registered", in.AgentID)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		agentPaths, err := listWorktreeAgents(worktree, s.projectPrefix(in.ProjectID))
		if err != nil {
			return nil, err
		}
		existingAgents := make([]model.Agent, 0, len(agentPaths))
		for _, existingPath := range agentPaths {
			var existing model.Agent
			if err := readWorktreeJSON(worktree, existingPath, &existing); err != nil {
				return nil, err
			}
			if err := model.ValidateAgent(existing); err != nil || existing.ProjectID != in.ProjectID {
				return nil, fmt.Errorf("invalid existing Agent record %q", existingPath)
			}
			existingAgents = append(existingAgents, existing)
		}
		if err := s.validateManagedRuntimeBindingAgainst(agent, existingAgents); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, path, agent); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	payload, err := json.Marshal(agent)
	if err != nil {
		return model.Agent{}, OperationResult{}, fmt.Errorf("encode Local Agent projection: %w", err)
	}
	if err := s.Durability.UpsertLocalAgent(ctx, sqlitestore.LocalAgent{
		ProjectID: in.ProjectID,
		AgentID:   agent.AgentID,
		Payload:   payload,
		UpdatedAt: agent.UpdatedAt.Format(time.RFC3339Nano),
	}); err != nil {
		_, rollbackErr := s.Hub.Transact(ctx, tx.After, "gateway: rollback Agent registration "+in.ProjectID+"/"+in.AgentID, func(worktree string) ([]string, error) {
			if removeErr := os.Remove(filepath.Join(worktree, filepath.FromSlash(path))); removeErr != nil && !os.IsNotExist(removeErr) {
				return nil, removeErr
			}
			return []string{path}, nil
		})
		if rollbackErr != nil {
			return model.Agent{}, OperationResult{}, fmt.Errorf("Agent registration left Hub/Local state inconsistent: projection=%v rollback=%v", err, rollbackErr)
		}
		return model.Agent{}, OperationResult{}, fmt.Errorf("write Local Agent projection: %w", err)
	}
	return agent, OperationResult{
		Hub:       tx,
		ProjectID: in.ProjectID,
		Status:    "registered",
	}, nil
}

func (s *Service) ensurePortableAgentWorkflowRole(ctx context.Context, existing model.Agent, workflowRole string) (model.Agent, error) {
	if err := validatePortableAgentWorkflowRole(workflowRole); err != nil {
		return model.Agent{}, err
	}
	if existing.WorkflowRole != "" {
		if existing.WorkflowRole != workflowRole {
			return model.Agent{}, fmt.Errorf("Agent %q is already assigned workflow role %q", existing.AgentID, existing.WorkflowRole)
		}
		return existing, nil
	}
	hubRevision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		return model.Agent{}, err
	}
	path := s.agentPath(existing.ProjectID, existing.AgentID)
	updated := existing
	tx, err := s.Hub.Transact(ctx, hubRevision, "gateway: materialize Agent workflow role "+existing.ProjectID+"/"+existing.AgentID, func(worktree string) ([]string, error) {
		if err := readWorktreeJSON(worktree, path, &updated); err != nil {
			return nil, err
		}
		if err := model.ValidateAgent(updated); err != nil || updated.ProjectID != existing.ProjectID || updated.AgentID != existing.AgentID {
			return nil, fmt.Errorf("invalid existing Agent")
		}
		if updated.WorkflowRole != "" {
			return nil, fmt.Errorf("Agent %q workflow role changed during bootstrap", updated.AgentID)
		}
		updated.WorkflowRole = workflowRole
		updatedAt := time.Now().UTC()
		if updatedAt.Before(updated.CreatedAt) {
			updatedAt = updated.CreatedAt
		}
		updated.UpdatedAt = updatedAt
		if err := hub.WriteJSON(worktree, path, updated); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		return model.Agent{}, err
	}
	if updated.WorkflowRole == "" {
		return updated, nil
	}
	payload, err := json.Marshal(updated)
	if err != nil {
		return model.Agent{}, fmt.Errorf("encode Local Agent projection: %w", err)
	}
	if s.Durability == nil {
		return updated, nil
	}
	if err := s.Durability.UpsertLocalAgent(ctx, sqlitestore.LocalAgent{
		ProjectID: existing.ProjectID,
		AgentID:   existing.AgentID,
		Payload:   payload,
		UpdatedAt: updated.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		_, rollbackErr := s.Hub.Transact(ctx, tx.After, "gateway: rollback Agent workflow role "+existing.ProjectID+"/"+existing.AgentID, func(worktree string) ([]string, error) {
			if err := hub.WriteJSON(worktree, path, existing); err != nil {
				return nil, err
			}
			return []string{path}, nil
		})
		if rollbackErr != nil {
			return model.Agent{}, fmt.Errorf("Agent workflow role left Hub/Local state inconsistent: projection=%v rollback=%v", err, rollbackErr)
		}
		return model.Agent{}, fmt.Errorf("write Local Agent projection: %w", err)
	}
	return updated, nil
}

func listWorktreeAgents(worktree, prefix string) ([]string, error) {
	root := filepath.Join(worktree, filepath.FromSlash(prefix+"/agents"))
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			paths = append(paths, prefix+"/agents/"+entry.Name())
		}
	}
	return paths, nil
}
