package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

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

// AgentRegister creates one Local project Agent.
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
	store := s.localStateStore()
	if store == nil {
		return model.Agent{}, OperationResult{}, fmt.Errorf("Local durability is required for Agent registration")
	}
	if _, err := s.EffectiveProjectConfig(in.ProjectID); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	if _, err := store.ReadLocalAgent(ctx, in.ProjectID, in.AgentID); err == nil {
		return model.Agent{}, OperationResult{}, fmt.Errorf("Agent %q is already registered", in.AgentID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return model.Agent{}, OperationResult{}, err
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
	if err := model.ValidateAgent(agent); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	if err := s.validateManagedRuntimeBindingCollision(ctx, agent); err != nil {
		return model.Agent{}, OperationResult{}, err
	}
	payload, err := json.Marshal(agent)
	if err != nil {
		return model.Agent{}, OperationResult{}, fmt.Errorf("encode Local Agent projection: %w", err)
	}
	if err := store.CreateLocalAgent(ctx, sqlitestore.LocalAgent{
		ProjectID: in.ProjectID,
		AgentID:   agent.AgentID,
		Payload:   payload,
		UpdatedAt: agent.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		return model.Agent{}, OperationResult{}, fmt.Errorf("write Local Agent projection: %w", err)
	}
	return agent, OperationResult{
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
	store := s.localStateStore()
	if store == nil {
		return model.Agent{}, fmt.Errorf("Local durability is required for Agent workflow role")
	}
	stored, err := store.ReadLocalAgent(ctx, existing.ProjectID, existing.AgentID)
	if err != nil {
		return model.Agent{}, err
	}
	updated := existing
	updated.WorkflowRole = workflowRole
	updatedAt := time.Now().UTC()
	if updatedAt.Before(updated.CreatedAt) {
		updatedAt = updated.CreatedAt
	}
	updated.UpdatedAt = updatedAt
	if err := model.ValidateAgent(updated); err != nil {
		return model.Agent{}, err
	}
	payload, err := json.Marshal(updated)
	if err != nil {
		return model.Agent{}, fmt.Errorf("encode Local Agent projection: %w", err)
	}
	if err := store.UpdateLocalAgent(ctx, sqlitestore.LocalAgent{
		ProjectID: existing.ProjectID,
		AgentID:   existing.AgentID,
		Payload:   payload,
		UpdatedAt: updated.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}, stored.Payload); err != nil {
		return model.Agent{}, fmt.Errorf("update Local Agent workflow role: %w", err)
	}
	return updated, nil
}
