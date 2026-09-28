package sqlitestore

import (
	"context"
	"encoding/json"
	"fmt"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func sharedProjectWorkflowPolicyForMigration(ctx context.Context, db *upstream.Store, projectID string) (model.ProjectWorkflowPolicy, error) {
	names := []string{"agent.wait_for_ci", "ci.release", "ci.task", "ci.task_merge", "integration_branch", "workflow_stage"}
	rows, err := db.Query(ctx, `SELECT payload FROM shared_rules WHERE json_extract(payload,'$.project_id')=? AND json_extract(payload,'$.name') IN ('agent.wait_for_ci','ci.release','ci.task','ci.task_merge','integration_branch','workflow_stage')`, projectID)
	if err != nil {
		return model.ProjectWorkflowPolicy{}, fmt.Errorf("read canonical ProjectWorkflowPolicy Rules: %w", err)
	}
	policy := model.ProjectWorkflowPolicy{SchemaVersion: model.SchemaVersion, ProjectID: projectID, Revision: 1}
	seen := make(map[string]bool, len(names))
	decode := func(rule model.Rule) error {
		if rule.ProjectID != projectID || rule.Status != model.RuleStatusAccepted || seen[rule.Name] {
			return fmt.Errorf("canonical ProjectWorkflowPolicy Rule %q is invalid", rule.Name)
		}
		seen[rule.Name] = true
		switch rule.Name {
		case "agent.wait_for_ci":
			return json.Unmarshal(rule.Value, &policy.Agent.WaitForCI)
		case "ci.release":
			return json.Unmarshal(rule.Value, &policy.CI.Release)
		case "ci.task":
			return json.Unmarshal(rule.Value, &policy.CI.Task)
		case "ci.task_merge":
			return json.Unmarshal(rule.Value, &policy.CI.TaskMerge)
		case "integration_branch":
			return json.Unmarshal(rule.Value, &policy.IntegrationBranch)
		case "workflow_stage":
			return json.Unmarshal(rule.Value, &policy.WorkflowStage)
		default:
			return fmt.Errorf("unexpected canonical ProjectWorkflowPolicy Rule %q", rule.Name)
		}
	}
	for _, row := range rows.Rows {
		if len(row) != 1 {
			return model.ProjectWorkflowPolicy{}, fmt.Errorf("invalid canonical ProjectWorkflowPolicy Rule row")
		}
		payload, ok := row[0].([]byte)
		if !ok {
			if text, isText := row[0].(string); isText {
				payload, ok = []byte(text), true
			}
		}
		if !ok {
			return model.ProjectWorkflowPolicy{}, fmt.Errorf("invalid canonical ProjectWorkflowPolicy Rule payload")
		}
		var rule model.Rule
		if err := json.Unmarshal(payload, &rule); err != nil {
			return model.ProjectWorkflowPolicy{}, err
		}
		if err := model.ValidateRule(rule); err != nil {
			return model.ProjectWorkflowPolicy{}, err
		}
		if err := decode(rule); err != nil {
			return model.ProjectWorkflowPolicy{}, err
		}
		if rule.UpdatedAt.After(policy.UpdatedAt) {
			policy.UpdatedAt = rule.UpdatedAt
			policy.UpdatedBy = rule.UpdatedBy
			if policy.UpdatedBy == "" {
				policy.UpdatedBy = rule.CreatedBy
			}
		}
	}
	for _, name := range names {
		if !seen[name] {
			return model.ProjectWorkflowPolicy{}, fmt.Errorf("canonical ProjectWorkflowPolicy Rule %q is missing", name)
		}
	}
	if policy.UpdatedAt.IsZero() || policy.UpdatedBy == "" {
		return model.ProjectWorkflowPolicy{}, fmt.Errorf("canonical ProjectWorkflowPolicy metadata is incomplete")
	}
	if err := model.ValidateProjectWorkflowPolicy(policy); err != nil {
		return model.ProjectWorkflowPolicy{}, err
	}
	return policy, nil
}
