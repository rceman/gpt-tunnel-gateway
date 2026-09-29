package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// WithPlannerWorkflowPolicyAuthority is a server-owned authority constructor.
// Callers cannot select an arbitrary role string.
func WithPlannerWorkflowPolicyAuthority(ctx context.Context) context.Context {
	return authority.WithPlanner(ctx)
}

// WithOperatorWorkflowPolicyAuthority is only for the local dispatcher. It
// is intentionally not accepted by workflow-policy mutation methods.
func WithOperatorWorkflowPolicyAuthority(ctx context.Context) context.Context {
	return authority.WithOperator(ctx)
}

func (s *Service) workflowPolicyPath(projectID string) string {
	if model.ValidateProjectIdentifier(projectID) != nil {
		return "../invalid-workflow-policy"
	}
	return s.projectPrefix(projectID) + "/workflow-policy/current.json"
}

func (s *Service) ProjectWorkflowPolicyRead(ctx context.Context, projectID string) (model.ProjectWorkflowPolicy, error) {
	retired, err := s.isProjectRetired(ctx, projectID)
	if err != nil {
		return model.ProjectWorkflowPolicy{}, err
	}
	if !retired {
		_, retired, err = s.readHubProjectRetirement(ctx, projectID)
		if err != nil {
			return model.ProjectWorkflowPolicy{}, err
		}
	}
	if retired {
		return model.ProjectWorkflowPolicy{}, fmt.Errorf("project %q is retired", projectID)
	}
	policy, _, err := s.projectWorkflowPolicyReadDetailed(ctx, projectID)
	if err == nil {
		_ = s.cacheProjectWorkflowPolicy(policy)
	}
	return policy, err
}

// ProjectWorkflowPolicyReadFast serves the validated local projection when it
// is already seeded, falling back to the canonical read only to seed or repair
// the cache. Read callers never receive an unvalidated cache entry.
func (s *Service) ProjectWorkflowPolicyReadFast(ctx context.Context, projectID string) (model.ProjectWorkflowPolicy, error) {
	retired, err := s.isProjectRetired(ctx, projectID)
	if err != nil {
		return model.ProjectWorkflowPolicy{}, err
	}
	if !retired {
		_, retired, err = s.readHubProjectRetirement(ctx, projectID)
		if err != nil {
			return model.ProjectWorkflowPolicy{}, err
		}
	}
	if retired {
		return model.ProjectWorkflowPolicy{}, fmt.Errorf("project %q is retired", projectID)
	}
	if s.Durability != nil {
		configuration, err := s.ProjectConfigurationRead(ctx, projectID)
		if err != nil {
			return model.ProjectWorkflowPolicy{}, err
		}
		return s.workflowPolicyFromAuthority(ctx, configuration)
	}
	if policy, err := s.CachedProjectWorkflowPolicy(projectID); err == nil {
		return policy, nil
	}
	return s.ProjectWorkflowPolicyRead(ctx, projectID)
}

func (s *Service) workflowPolicyFromAuthority(ctx context.Context, configuration model.ProjectConfiguration) (model.ProjectWorkflowPolicy, error) {
	if s.Durability == nil {
		return s.readHubWorkflowPolicy(ctx, configuration.ProjectID)
	}
	effective, _, err := s.ruleEffectiveSetShared(ctx, configuration.ProjectID)
	if err != nil {
		return model.ProjectWorkflowPolicy{}, err
	}
	return workflowPolicyFromEffectiveRules(configuration, effective)
}

func workflowPolicyFromEffectiveRules(configuration model.ProjectConfiguration, effective []model.Rule) (model.ProjectWorkflowPolicy, error) {
	leaves := make(map[string]json.RawMessage, len(effective))
	for _, rule := range effective {
		leaves[rule.Name] = rule.Value
	}
	policy := model.ProjectWorkflowPolicy{
		SchemaVersion: model.SchemaVersion,
		ProjectID:     configuration.ProjectID,
		Revision:      configuration.Revision,
		UpdatedBy:     configuration.UpdatedBy,
		UpdatedAt:     configuration.UpdatedAt,
	}
	decode := func(name string, target any) error {
		raw, ok := leaves[name]
		if !ok {
			return fmt.Errorf("required effective rule %q is missing", name)
		}
		if err := json.Unmarshal(raw, target); err != nil {
			return fmt.Errorf("required effective rule %q is invalid: %w", name, err)
		}
		return nil
	}
	if err := decode("workflow_stage", &policy.WorkflowStage); err != nil {
		return model.ProjectWorkflowPolicy{}, err
	}
	if err := decode("integration_branch", &policy.IntegrationBranch); err != nil {
		return model.ProjectWorkflowPolicy{}, err
	}
	if err := decode("agent.wait_for_ci", &policy.Agent.WaitForCI); err != nil {
		return model.ProjectWorkflowPolicy{}, err
	}
	if err := decode("ci.release", &policy.CI.Release); err != nil {
		return model.ProjectWorkflowPolicy{}, err
	}
	if err := decode("ci.task", &policy.CI.Task); err != nil {
		return model.ProjectWorkflowPolicy{}, err
	}
	if err := decode("ci.task_merge", &policy.CI.TaskMerge); err != nil {
		return model.ProjectWorkflowPolicy{}, err
	}
	if err := model.ValidateProjectWorkflowPolicy(policy); err != nil {
		return model.ProjectWorkflowPolicy{}, err
	}
	return policy, nil
}

func (s *Service) readHubWorkflowPolicy(ctx context.Context, projectID string) (model.ProjectWorkflowPolicy, error) {
	data, err := s.Hub.ReadFile(ctx, s.workflowPolicyPath(projectID))
	if err != nil {
		return model.ProjectWorkflowPolicy{}, err
	}
	var policy model.ProjectWorkflowPolicy
	if err := decodeStrict(data, &policy); err != nil {
		return model.ProjectWorkflowPolicy{}, err
	}
	if err := model.ValidateProjectWorkflowPolicy(policy); err != nil {
		return model.ProjectWorkflowPolicy{}, err
	}
	if policy.ProjectID != projectID {
		return model.ProjectWorkflowPolicy{}, fmt.Errorf("workflow policy project_id mismatch")
	}
	return policy, nil
}

func workflowPolicyLeavesEquivalent(left, right model.ProjectWorkflowPolicy) bool {
	return left.WorkflowStage == right.WorkflowStage && left.IntegrationBranch == right.IntegrationBranch && left.Agent.WaitForCI == right.Agent.WaitForCI && left.CI == right.CI
}

func workflowPoliciesEquivalent(left, right model.ProjectWorkflowPolicy) bool {
	return left.ProjectID == right.ProjectID && left.Revision == right.Revision && workflowPolicyLeavesEquivalent(left, right)
}

func (s *Service) projectWorkflowPolicyReadDetailed(ctx context.Context, projectID string) (model.ProjectWorkflowPolicy, string, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.ProjectWorkflowPolicy{}, "", err
	}
	if s.Durability == nil {
		policy, err := s.readHubWorkflowPolicy(ctx, projectID)
		if err != nil {
			return model.ProjectWorkflowPolicy{}, "", err
		}
		return policy, "workflow_policy", nil
	}
	configuration, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		return model.ProjectWorkflowPolicy{}, "", err
	}
	policy, err := s.workflowPolicyFromAuthority(ctx, configuration)
	if err != nil {
		return model.ProjectWorkflowPolicy{}, "", fmt.Errorf("project workflow authority is invalid: %w", err)
	}
	return policy, "shared_rules", nil
}
