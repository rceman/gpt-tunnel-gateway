package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const (
	WorkflowPolicyAuthorizationOperator = "operator"
	WorkflowPolicyAuthorizationPlanner  = "planner"
)

func validateWorkflowPolicyAuthorization(value string) error {
	if value != WorkflowPolicyAuthorizationOperator && value != WorkflowPolicyAuthorizationPlanner {
		return fmt.Errorf("workflow policy write requires authorization_context operator or planner")
	}
	return nil
}

func (s *Service) workflowPolicyPath(projectID string) string {
	if model.ValidateProjectIdentifier(projectID) != nil {
		return "../invalid-workflow-policy"
	}
	return s.projectPrefix(projectID) + "/workflow-policy/current.json"
}

func (s *Service) ProjectWorkflowPolicyRead(ctx context.Context, projectID string) (model.ProjectWorkflowPolicy, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.ProjectWorkflowPolicy{}, err
	}
	var policy model.ProjectWorkflowPolicy
	if err := s.Hub.ReadJSON(ctx, s.workflowPolicyPath(projectID), &policy); err != nil {
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

func (s *Service) ProjectWorkflowPolicyAdopt(ctx context.Context, in ProjectWorkflowPolicyInput) (model.ProjectWorkflowPolicy, OperationResult, error) {
	if err := validateWorkflowPolicyAuthorization(in.AuthorizationContext); err != nil {
		return model.ProjectWorkflowPolicy{}, OperationResult{}, err
	}
	policy := in.Policy
	if err := model.ValidateProjectWorkflowPolicy(policy); err != nil {
		return model.ProjectWorkflowPolicy{}, OperationResult{}, err
	}
	if _, err := s.ProjectRead(ctx, policy.ProjectID); err != nil {
		return model.ProjectWorkflowPolicy{}, OperationResult{}, err
	}
	path := s.workflowPolicyPath(policy.ProjectID)
	status := "adopted"
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: adopt workflow policy "+policy.ProjectID, func(worktree string) ([]string, error) {
		projectPath := s.projectPath(policy.ProjectID)
		var project model.Project
		if err := readWorktreeJSON(worktree, projectPath, &project); err != nil {
			return nil, fmt.Errorf("project %q is not durable: %w", policy.ProjectID, err)
		}
		if err := model.ValidateProject(project); err != nil || project.ID != policy.ProjectID {
			if err == nil {
				err = fmt.Errorf("project ID mismatch")
			}
			return nil, fmt.Errorf("project %q is invalid: %w", policy.ProjectID, err)
		}
		absolute := filepath.Join(worktree, filepath.FromSlash(path))
		if _, err := os.Lstat(absolute); err == nil {
			var current model.ProjectWorkflowPolicy
			if err := readWorktreeJSON(worktree, path, &current); err != nil {
				return nil, fmt.Errorf("read current workflow policy: %w", err)
			}
			if err := model.ValidateProjectWorkflowPolicy(current); err != nil {
				return nil, fmt.Errorf("current workflow policy is invalid: %w", err)
			}
			if policy.Revision != current.Revision+1 {
				return nil, fmt.Errorf("workflow policy revision must advance from %d to %d", current.Revision, policy.Revision)
			}
			status = "updated"
		} else if !os.IsNotExist(err) {
			return nil, err
		} else if policy.Revision != 1 {
			return nil, fmt.Errorf("initial workflow policy revision must be 1")
		}
		if err := hub.WriteJSON(worktree, path, policy); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		return model.ProjectWorkflowPolicy{}, OperationResult{}, err
	}
	return policy, OperationResult{Hub: tx, ProjectID: policy.ProjectID, Status: status}, nil
}

func (s *Service) ProjectWorkflowPolicyUpdate(ctx context.Context, in ProjectWorkflowPolicyInput) (model.ProjectWorkflowPolicy, OperationResult, error) {
	return s.ProjectWorkflowPolicyAdopt(ctx, in)
}

func rejectHostedCIGates(gates []string, mode string) error {
	if mode == model.WorkflowCIModeRequire {
		return nil
	}
	for _, gate := range gates {
		lower := strings.ToLower(gate)
		if strings.Contains(lower, "check-github-ci") || strings.Contains(lower, "github actions") || strings.Contains(lower, "hosted ci") || strings.Contains(lower, "wait for ci") || strings.Contains(lower, "require ci") || strings.Contains(lower, "ci required") || strings.Contains(lower, "--policy required") || strings.Contains(lower, "--wait") {
			return fmt.Errorf("required_gates contains hosted-CI wait/require semantics while policy mode is %s", mode)
		}
	}
	return nil
}

func (s *Service) deriveTaskWorkflowPolicy(ctx context.Context, projectID, operationClass string, gates []string) (model.ProjectWorkflowPolicy, model.EffectiveWorkflowPolicy, error) {
	if err := model.ValidateOperationClass(operationClass); err != nil {
		return model.ProjectWorkflowPolicy{}, model.EffectiveWorkflowPolicy{}, err
	}
	policy, err := s.ProjectWorkflowPolicyRead(ctx, projectID)
	if err != nil {
		return model.ProjectWorkflowPolicy{}, model.EffectiveWorkflowPolicy{}, fmt.Errorf("project workflow policy is required: %w", err)
	}
	effective, err := model.WorkflowPolicyForOperation(policy, operationClass)
	if err != nil {
		return model.ProjectWorkflowPolicy{}, model.EffectiveWorkflowPolicy{}, err
	}
	if err := rejectHostedCIGates(gates, effective.EffectiveCIMode); err != nil {
		return model.ProjectWorkflowPolicy{}, model.EffectiveWorkflowPolicy{}, err
	}
	return policy, effective, nil
}

func workflowPolicyStatus(policy model.ProjectWorkflowPolicy, err error, plan model.Plan, tasks []TaskRecord) ProjectWorkflowPolicyStatus {
	if err != nil {
		if IsNotFound(err) {
			return ProjectWorkflowPolicyStatus{State: "missing", Conflicts: []string{"workflow_policy_missing"}, CorrectiveAction: "adopt a durable project workflow policy before creating or superseding tasks"}
		}
		return ProjectWorkflowPolicyStatus{State: "invalid", Conflicts: []string{"workflow_policy_invalid"}, CorrectiveAction: "repair or re-adopt the durable project workflow policy"}
	}
	status := ProjectWorkflowPolicyStatus{State: "adopted", Revision: policy.Revision, WorkflowStage: policy.WorkflowStage, IntegrationBranch: policy.IntegrationBranch, AgentWaitForCI: policy.Agent.WaitForCI, CI: policy.CI, Conflicts: []string{}, CorrectiveAction: "none"}
	for _, item := range tasks {
		if item.Task.ID != plan.ActiveTaskID || item.Task.OperationClass == "" {
			continue
		}
		effective, effectiveErr := model.WorkflowPolicyForOperation(policy, item.Task.OperationClass)
		if effectiveErr != nil {
			status.State = "invalid"
			status.Conflicts = append(status.Conflicts, "active_task_policy_invalid")
			status.CorrectiveAction = "supersede or repair the active task under the durable project policy"
			break
		}
		status.ActiveOperationClass = effective.OperationClass
		status.ActiveCIMode = effective.EffectiveCIMode
		status.CIBlocking = effective.CIBlocking
	}
	return status
}
