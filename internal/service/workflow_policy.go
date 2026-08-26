package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// WithPlannerWorkflowPolicyAuthority and WithDeliveryWorkflowPolicyAuthority
// are server-owned authority constructors. Callers cannot select an arbitrary
// role string; transport adapters must obtain the appropriate context from
// the trusted Planner or Delivery boundary before invoking a mutating
// service.
func WithPlannerWorkflowPolicyAuthority(ctx context.Context) context.Context {
	return authority.WithPlanner(ctx)
}

func WithDeliveryWorkflowPolicyAuthority(ctx context.Context) context.Context {
	return authority.WithDelivery(ctx)
}

// WithOperatorWorkflowPolicyAuthority is only for the local dispatcher. It
// is intentionally not accepted by workflow-policy mutation methods.
func WithOperatorWorkflowPolicyAuthority(ctx context.Context) context.Context {
	return authority.WithOperator(ctx)
}

func RequireWorkflowPolicyAuthority(ctx context.Context) error {
	return authority.RequirePlannerOrDelivery(ctx)
}

// RequireOnboardingAuthority accepts only server-owned Planner/Delivery
// authority or the local dispatcher-owned operator context. Serialized role
// fields are never consulted.
func RequireOnboardingAuthority(ctx context.Context) error {
	return authority.RequireOnboarding(ctx)
}

func (s *Service) workflowPolicyPath(projectID string) string {
	if model.ValidateProjectIdentifier(projectID) != nil {
		return "../invalid-workflow-policy"
	}
	return s.projectPrefix(projectID) + "/workflow-policy/current.json"
}

func (s *Service) ProjectWorkflowPolicyRead(ctx context.Context, projectID string) (model.ProjectWorkflowPolicy, error) {
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
	if policy, err := s.CachedProjectWorkflowPolicy(projectID); err == nil {
		return policy, nil
	}
	return s.ProjectWorkflowPolicyRead(ctx, projectID)
}

func workflowPolicyFromConfiguration(configuration model.ProjectConfiguration) (model.ProjectWorkflowPolicy, error) {
	policy := model.ProjectWorkflowPolicy{
		SchemaVersion:     model.SchemaVersion,
		ProjectID:         configuration.ProjectID,
		Revision:          configuration.Revision,
		WorkflowStage:     configuration.Workflow.WorkflowStage,
		IntegrationBranch: configuration.Workflow.IntegrationBranch,
		Agent:             model.WorkflowPolicyAgent{WaitForCI: configuration.Workflow.WaitForCI},
		CI:                configuration.Workflow.CI,
		Gates:             append([]string{}, configuration.Workflow.Gates...),
		UpdatedBy:         configuration.UpdatedBy,
		UpdatedAt:         configuration.UpdatedAt,
	}
	if err := model.ValidateProjectWorkflowPolicy(policy); err != nil {
		return model.ProjectWorkflowPolicy{}, err
	}
	return policy, nil
}

func workflowPoliciesEquivalent(left, right model.ProjectWorkflowPolicy) bool {
	if left.ProjectID != right.ProjectID || left.Revision != right.Revision || left.WorkflowStage != right.WorkflowStage || left.IntegrationBranch != right.IntegrationBranch || left.Agent.WaitForCI != right.Agent.WaitForCI || left.CI != right.CI {
		return false
	}
	leftGates := model.EffectiveProjectWorkflowGates(left.Gates)
	rightGates := model.EffectiveProjectWorkflowGates(right.Gates)
	if len(leftGates) != len(rightGates) {
		return false
	}
	for i := range leftGates {
		if leftGates[i] != rightGates[i] {
			return false
		}
	}
	return true
}

func (s *Service) projectWorkflowPolicyReadDetailed(ctx context.Context, projectID string) (model.ProjectWorkflowPolicy, string, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.ProjectWorkflowPolicy{}, "", err
	}
	if s.Durability == nil {
		return model.ProjectWorkflowPolicy{}, "", fmt.Errorf("Shared workflow policy authority is unavailable")
	}
	configuration, configurationErr := s.ProjectConfigurationRead(ctx, projectID)
	var canonical model.ProjectWorkflowPolicy
	if configurationErr == nil {
		var err error
		canonical, err = workflowPolicyFromConfiguration(configuration)
		if err != nil {
			return model.ProjectWorkflowPolicy{}, "", fmt.Errorf("project configuration workflow is invalid: %w", err)
		}
		return canonical, "project_configuration", nil
	}
	// Shared project configuration is the sole workflow-policy authority after
	// cutover. In particular, do not fall back to the legacy Hub policy file:
	// doing so would make a failed local authority read appear healthy and would
	// reintroduce Hub I/O into normal execution.
	return model.ProjectWorkflowPolicy{}, "", configurationErr
}
