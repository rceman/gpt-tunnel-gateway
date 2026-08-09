package mcp

import (
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (f *canonicalOutputFixture) populateCanonicalProjectPlan() {
	now := f.now
	project := model.Project{SchemaVersion: 1, ID: "project", RepositoryURL: "git@example.invalid:project.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active", CreatedAt: now, UpdatedAt: now}
	plan := model.Plan{SchemaVersion: model.PlanSchemaVersion, ProjectID: "project", Revision: 1, Title: "title", Summary: "summary", CurrentObjective: "objective", Queue: []string{}, Sections: []model.PlanSectionIndex{}, UpdatedBy: "gpt", UpdatedAt: now}
	section := model.PlanSection{SchemaVersion: model.PlanSchemaVersion, ProjectID: "project", ID: "section", Revision: 1, Title: "section", ShortDescription: "short", Description: "description", UpdatedBy: "gpt", UpdatedAt: now}
	render := model.PlanRender{SchemaVersion: model.PlanSchemaVersion, ProjectID: "project", Revision: 1, Title: "title", Summary: "summary", CurrentObjective: "objective", Text: "rendered"}
	adr := model.ADR{SchemaVersion: 1, ID: "ADR-TEST", ProjectID: "project", Title: "title", Status: "accepted", Context: "context", Decision: "decision", Consequences: "consequences", CreatedAt: now}
	policy := model.ProjectWorkflowPolicy{SchemaVersion: model.SchemaVersion, ProjectID: "project", Revision: 1, WorkflowStage: model.WorkflowStageTransitionalMain, IntegrationBranch: "main", Agent: model.WorkflowPolicyAgent{WaitForCI: false}, CI: model.WorkflowPolicyCI{Task: model.WorkflowCIModeDisabled, TaskMerge: model.WorkflowCIModeObserve, Release: model.WorkflowCIModeObserve}, UpdatedBy: "gpt", UpdatedAt: now}
	task := model.Task{SchemaVersion: 1, ID: "task", SHA256: strings.Repeat("b", 64), ProjectID: "project", Title: "title", Objective: "objective", Branch: "feature/x", BaseRevision: strings.Repeat("c", 40), AcceptanceCriteria: []string{}, Constraints: []string{}, WorkflowPolicyRevision: 1, OperationClass: "implementation", EffectiveCIField: "task", EffectiveCIMode: model.WorkflowCIModeDisabled, Status: "created", CreatedBy: "gpt", CreatedAt: now}
	state := model.TaskState{SchemaVersion: 1, TaskID: "task", TaskSHA256: task.SHA256, Status: "created", UpdatedAt: now}
	f.project = project
	f.plan = plan
	f.section = section
	f.render = render
	f.adr = adr
	f.policy = policy
	f.task = task
	f.state = state
}
