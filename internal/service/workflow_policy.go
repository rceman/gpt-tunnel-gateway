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

// workflowPolicyFromConfiguration derives the workflow policy directly from
// the configuration document. It is the legacy-compatibility path used when
// Shared durability is absent. Under Shared durability the configuration
// document's workflow leaf fields (workflow_stage, integration_branch,
// agent.wait_for_ci, ci.release, ci.task, ci.task_merge) are provenance
// retained for the seed migration — the named-rule effective set is the only
// authority for them.
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

// workflowPolicyFromAuthority derives the effective workflow policy for a
// configured project. Under Shared durability the six machine-policy leaves
// are sourced exclusively from the named-rule effective set; the
// configuration document contributes only non-rule fields (gates,
// revision/updated provenance). Without Shared durability the configuration
// document remains the leaf authority.
func (s *Service) workflowPolicyFromAuthority(ctx context.Context, configuration model.ProjectConfiguration) (model.ProjectWorkflowPolicy, error) {
	if s.Durability == nil {
		return workflowPolicyFromConfiguration(configuration)
	}
	effective, _, err := s.ruleEffectiveSetShared(ctx, configuration.ProjectID)
	if err != nil {
		return model.ProjectWorkflowPolicy{}, err
	}
	return workflowPolicyFromEffectiveRules(configuration, effective)
}

// workflowPolicyFromEffectiveRules composes the effective workflow policy
// from the named-rule effective set. Every machine leaf is required; a
// missing or invalid leaf fails closed so a configured project can never run
// on a vacuous or half-seeded effective set.
func workflowPolicyFromEffectiveRules(configuration model.ProjectConfiguration, effective []model.Rule) (model.ProjectWorkflowPolicy, error) {
	leaves := make(map[string]json.RawMessage, len(effective))
	for _, rule := range effective {
		leaves[rule.Name] = rule.Value
	}
	policy := model.ProjectWorkflowPolicy{
		SchemaVersion: model.SchemaVersion,
		ProjectID:     configuration.ProjectID,
		Revision:      configuration.Revision,
		Gates:         append([]string{}, configuration.Workflow.Gates...),
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

// workflowPolicyLeavesEquivalent compares the six rule-governed machine
// leaves between two policies; provenance fields (gates, revision, updated
// metadata) are excluded.
func workflowPolicyLeavesEquivalent(left, right model.ProjectWorkflowPolicy) bool {
	return left.WorkflowStage == right.WorkflowStage && left.IntegrationBranch == right.IntegrationBranch && left.Agent.WaitForCI == right.Agent.WaitForCI && left.CI == right.CI
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
	configuration, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		return model.ProjectWorkflowPolicy{}, "", err
	}
	policy, err := s.workflowPolicyFromAuthority(ctx, configuration)
	if err != nil {
		return model.ProjectWorkflowPolicy{}, "", fmt.Errorf("project workflow authority is invalid: %w", err)
	}
	return policy, "project_configuration", nil
}
