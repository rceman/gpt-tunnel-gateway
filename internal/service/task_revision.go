package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type TaskCorrectionCreateInput struct {
	TaskID             string   `json:"task_id"`
	SourceRevisionID   string   `json:"source_revision_id"`
	SourceRunID        string   `json:"source_run_id"`
	SourceReportID     string   `json:"source_report_id"`
	Title              string   `json:"title,omitempty"`
	Objective          string   `json:"objective,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	Constraints        []string `json:"constraints,omitempty"`
	RequiredGates      []string `json:"required_gates,omitempty"`
	CreatedBy          string   `json:"created_by"`
	WriteOptions
}

func (s *Service) taskRevisionPrefix(project, taskID string) string {
	if model.ValidateProjectIdentifier(project) != nil || model.ValidateCanonicalTaskID(taskID) != nil {
		return "../invalid-task-revisions"
	}
	return s.projectPrefix(project) + "/tasks/" + taskID + "/revisions"
}

func (s *Service) taskRevisionPath(project, taskID string, revision int) string {
	id, err := model.FormatTaskRevisionID(taskID, revision)
	if err != nil {
		return "../invalid-task-revision"
	}
	return s.taskRevisionPrefix(project, taskID) + "/" + id + ".json"
}

func (s *Service) readTaskRevision(ctx context.Context, project, taskID string, revision int) (model.TaskRevision, error) {
	task, err := s.findTask(ctx, taskID)
	if err != nil {
		return model.TaskRevision{}, err
	}
	if revision == 1 {
		legacy := model.TaskRevisionFromTask(task)
		if err := model.ValidateTaskRevision(legacy); err != nil {
			return model.TaskRevision{}, err
		}
		return legacy, nil
	}
	var result model.TaskRevision
	if err := s.Hub.ReadJSON(ctx, s.taskRevisionPath(project, taskID, revision), &result); err != nil {
		return model.TaskRevision{}, err
	}
	if err := model.ValidateTaskRevision(result); err != nil {
		return model.TaskRevision{}, err
	}
	if result.ProjectID != project || result.TaskID != taskID || result.TaskRevision != revision {
		return model.TaskRevision{}, fmt.Errorf("task revision ownership mismatch")
	}
	hash, err := model.HashTaskRevision(result)
	if err != nil || hash != result.RevisionSHA256 {
		return model.TaskRevision{}, fmt.Errorf("task revision hash mismatch")
	}
	return result, nil
}

func (s *Service) taskRevisionListForTask(ctx context.Context, task model.Task) ([]model.TaskRevision, error) {
	result := []model.TaskRevision{model.TaskRevisionFromTask(task)}
	paths, err := s.Hub.List(ctx, s.taskRevisionPrefix(task.ProjectID, task.ID), ".json")
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		var item model.TaskRevision
		if err := s.Hub.ReadJSON(ctx, path, &item); err != nil {
			return nil, err
		}
		if err := model.ValidateTaskRevision(item); err != nil {
			return nil, err
		}
		hash, err := model.HashTaskRevision(item)
		if err != nil || hash != item.RevisionSHA256 {
			return nil, fmt.Errorf("task revision hash mismatch")
		}
		if item.ProjectID != task.ProjectID || item.TaskID != task.ID {
			return nil, fmt.Errorf("task revision ownership mismatch")
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].TaskRevision < result[j].TaskRevision })
	for i, item := range result {
		if item.TaskRevision != i+1 {
			return nil, fmt.Errorf("task revisions are not contiguous")
		}
		if i > 0 && (item.ParentTaskRevision != result[i-1].TaskRevision || item.ParentTaskSHA256 != result[i-1].RevisionSHA256) {
			return nil, fmt.Errorf("task revision parent binding mismatch")
		}
	}
	return result, nil
}

func (s *Service) currentTaskRevision(ctx context.Context, task model.Task) (model.TaskRevision, error) {
	items, err := s.taskRevisionListForTask(ctx, task)
	if err != nil {
		return model.TaskRevision{}, err
	}
	return items[len(items)-1], nil
}

func (s *Service) TaskRevisionList(ctx context.Context, taskID string) ([]model.TaskRevision, error) {
	if base, _, err := model.ParseTaskRevisionID(taskID); err == nil {
		taskID = base
	}
	task, err := s.findTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return s.taskRevisionListForTask(ctx, task)
}

func (s *Service) TaskRevisionRead(ctx context.Context, revisionID string) (model.TaskRevision, error) {
	taskID, revision, err := model.ParseTaskRevisionID(revisionID)
	if err != nil {
		return model.TaskRevision{}, err
	}
	task, err := s.findTask(ctx, taskID)
	if err != nil {
		return model.TaskRevision{}, err
	}
	return s.readTaskRevision(ctx, task.ProjectID, taskID, revision)
}

func (s *Service) TaskRevisionStatus(ctx context.Context, revisionID string) (model.TaskRevisionStatus, error) {
	revision, err := s.TaskRevisionRead(ctx, revisionID)
	if err != nil {
		return model.TaskRevisionStatus{}, err
	}
	return revision.StatusView(), nil
}

func correctionEligibleReport(report model.RunReviewReport) bool {
	if report.Outcome == model.ReviewOutcomeRejected {
		return true
	}
	if report.Outcome != model.ReviewOutcomeAccepted {
		return false
	}
	for _, finding := range report.Findings {
		if finding.Severity != "info" {
			return true
		}
	}
	return len(report.Findings) > 0
}

func revisionSourceRunMatches(taskID string, source model.TaskRevision, run model.Run) bool {
	if run.TaskRevision == source.TaskRevision && run.TaskRevisionSHA256 == source.RevisionSHA256 && run.TaskRunNumber > 0 {
		return true
	}
	if source.TaskRevision != 1 || run.TaskRevision != 0 || run.TaskRevisionSHA256 != "" || run.TaskRunNumber != 0 {
		return false
	}
	parsedTask, _, err := model.ParseRunID(run.ID)
	return err == nil && parsedTask == taskID
}

func revisionSourceReportMatches(source model.TaskRevision, run model.Run, report model.RunReviewReport) bool {
	if report.TaskRevision == source.TaskRevision && report.TaskRevisionSHA256 == source.RevisionSHA256 && report.TaskRunNumber == run.TaskRunNumber {
		return true
	}
	return source.TaskRevision == 1 && report.TaskRevision == 0 && report.TaskRevisionSHA256 == "" && report.TaskRunNumber == 0
}

func (s *Service) TaskCorrectionCreate(ctx context.Context, in TaskCorrectionCreateInput) (model.TaskRevision, OperationResult, error) {
	if err := requireCanonicalTaskID(in.TaskID); err != nil {
		return model.TaskRevision{}, OperationResult{}, err
	}
	if in.SourceRevisionID == "" || in.SourceRunID == "" || in.SourceReportID == "" {
		return model.TaskRevision{}, OperationResult{}, fmt.Errorf("source revision, run and report are required")
	}
	task, err := s.findTask(ctx, in.TaskID)
	if err != nil {
		return model.TaskRevision{}, OperationResult{}, err
	}
	baseID, sourceNumber, err := model.ParseTaskRevisionID(in.SourceRevisionID)
	if err != nil || baseID != task.ID {
		return model.TaskRevision{}, OperationResult{}, fmt.Errorf("source revision does not belong to task")
	}
	source, err := s.readTaskRevision(ctx, task.ProjectID, task.ID, sourceNumber)
	if err != nil {
		return model.TaskRevision{}, OperationResult{}, err
	}
	current, err := s.currentTaskRevision(ctx, task)
	if err != nil {
		return model.TaskRevision{}, OperationResult{}, err
	}
	if current.ID != source.ID {
		return model.TaskRevision{}, OperationResult{}, fmt.Errorf("source revision is not current")
	}
	run, err := s.findRun(ctx, in.SourceRunID)
	if err != nil {
		return model.TaskRevision{}, OperationResult{}, err
	}
	if run.TaskID != task.ID || !revisionSourceRunMatches(task.ID, source, run) || run.ID != in.SourceRunID || operationalActiveRun(run) {
		return model.TaskRevision{}, OperationResult{}, fmt.Errorf("source run is not the exact terminal revision run")
	}
	var delivery model.RunReviewReport
	if err := s.Hub.ReadJSON(ctx, s.reviewReportPath(task.ProjectID, run.ID), &delivery); err != nil {
		return model.TaskRevision{}, OperationResult{}, err
	}
	if err := model.ValidateRunReviewReport(delivery); err != nil || delivery.ID != in.SourceReportID || delivery.TaskID != task.ID || delivery.RunID != run.ID || !revisionSourceReportMatches(source, run, delivery) || !correctionEligibleReport(delivery) {
		return model.TaskRevision{}, OperationResult{}, fmt.Errorf("source Delivery report is not correction-eligible")
	}
	state, err := s.taskState(ctx, task)
	if err != nil {
		return model.TaskRevision{}, OperationResult{}, err
	}
	switch state.Status {
	case "completed", "merge_ready", "deferred":
	case "ready":
		if delivery.Outcome != model.ReviewOutcomeRejected {
			return model.TaskRevision{}, OperationResult{}, fmt.Errorf("task state %q is not correction-eligible", state.Status)
		}
	case "created", "dispatched", "merged", "released", "activated":
		return model.TaskRevision{}, OperationResult{}, fmt.Errorf("task state %q is not correction-eligible", state.Status)
	default:
		return model.TaskRevision{}, OperationResult{}, fmt.Errorf("task state %q is not correction-eligible", state.Status)
	}
	plan, err := s.PlanRead(ctx, task.ProjectID)
	if err != nil {
		return model.TaskRevision{}, OperationResult{}, err
	}
	if plan.ActiveTaskID == task.ID || plan.ActiveRunID == run.ID {
		return model.TaskRevision{}, OperationResult{}, fmt.Errorf("task correction requires cleared current activity")
	}
	local, err := s.projectConfig(task.ProjectID)
	if err != nil {
		return model.TaskRevision{}, OperationResult{}, err
	}
	head, branch, clean, err := s.Git.CurrentHead(ctx, local)
	if err != nil {
		return model.TaskRevision{}, OperationResult{}, err
	}
	if !clean || branch != source.Branch || head != delivery.ReviewedHead {
		return model.TaskRevision{}, OperationResult{}, fmt.Errorf("task correction requires the exact clean reviewed branch head")
	}
	title, objective := source.Title, source.Objective
	acceptance, constraints, gates := source.AcceptanceCriteria, source.Constraints, source.RequiredGates
	if in.Title != "" {
		title = in.Title
	}
	if in.Objective != "" {
		objective = in.Objective
	}
	if in.AcceptanceCriteria != nil {
		acceptance = append([]string{}, in.AcceptanceCriteria...)
	}
	if in.Constraints != nil {
		constraints = append([]string{}, in.Constraints...)
	}
	if in.RequiredGates != nil {
		gates = append([]string{}, in.RequiredGates...)
	}
	now := time.Now().UTC()
	candidate := model.TaskRevision{SchemaVersion: model.TaskRevisionSchemaVersion, ID: model.FormatTaskRevisionIDUnchecked(task.ID, source.TaskRevision+1), TaskID: task.ID, TaskRevision: source.TaskRevision + 1, ParentTaskRevision: source.TaskRevision, ParentTaskSHA256: source.RevisionSHA256, ProjectID: task.ProjectID, Title: title, Objective: objective, Branch: source.Branch, BaseRevision: source.BaseRevision, AcceptanceCriteria: acceptance, Constraints: constraints, RequiredGates: gates, WorkflowPolicyRevision: source.WorkflowPolicyRevision, OperationClass: source.OperationClass, EffectiveCIField: source.EffectiveCIField, EffectiveCIMode: source.EffectiveCIMode, WaitForCI: source.WaitForCI, CIBlocking: source.CIBlocking, AgentMayWait: source.AgentMayWait, Status: "created", SourceRunID: run.ID, SourceReportID: delivery.ID, CreatedBy: in.CreatedBy, CreatedAt: now}
	candidate.RevisionSHA256, err = model.HashTaskRevision(candidate)
	if err != nil {
		return model.TaskRevision{}, OperationResult{}, err
	}
	if err := model.ValidateTaskRevision(candidate); err != nil {
		return model.TaskRevision{}, OperationResult{}, err
	}
	if in.ExpectedHubRevision == "" {
		in.ExpectedHubRevision, err = s.hubRevision(ctx)
		if err != nil {
			return model.TaskRevision{}, OperationResult{}, err
		}
	}
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: create task revision "+candidate.ID, func(worktree string) ([]string, error) {
		var root model.Task
		if err := readWorktreeJSON(worktree, s.taskPath(task.ProjectID, task.ID), &root); err != nil {
			return nil, err
		}
		if root.SHA256 != task.SHA256 || root.ID != task.ID {
			return nil, fmt.Errorf("task changed concurrently")
		}
		var currentState model.TaskState
		if err := readWorktreeJSON(worktree, s.taskStatePath(task.ProjectID, task.ID), &currentState); err != nil {
			return nil, err
		}
		if err := model.ValidateTaskState(currentState, root); err != nil {
			return nil, err
		}
		if currentState.Status != state.Status {
			return nil, fmt.Errorf("task state changed concurrently")
		}
		var currentPlan model.Plan
		if err := readWorktreeJSON(worktree, s.planPath(task.ProjectID), &currentPlan); err != nil {
			return nil, err
		}
		if currentPlan.ActiveTaskID == task.ID || currentPlan.ActiveRunID == run.ID {
			return nil, fmt.Errorf("task activity changed before correction")
		}
		var currentRun model.Run
		if err := readWorktreeJSON(worktree, s.runPath(task.ProjectID, run.ID), &currentRun); err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(currentRun, run) {
			return nil, fmt.Errorf("source run changed concurrently")
		}
		var currentDelivery model.RunReviewReport
		if err := readWorktreeJSON(worktree, s.reviewReportPath(task.ProjectID, run.ID), &currentDelivery); err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(currentDelivery, delivery) {
			return nil, fmt.Errorf("source Delivery report changed concurrently")
		}
		if sourceNumber > 1 {
			var stored model.TaskRevision
			if err := readWorktreeJSON(worktree, s.taskRevisionPath(task.ProjectID, task.ID, sourceNumber), &stored); err != nil {
				return nil, err
			}
			if stored.RevisionSHA256 != source.RevisionSHA256 || stored.ID != source.ID {
				return nil, fmt.Errorf("source revision changed concurrently")
			}
		}
		target := filepath.Join(worktree, filepath.FromSlash(s.taskRevisionPath(task.ProjectID, task.ID, candidate.TaskRevision)))
		if _, err := os.Lstat(target); err == nil {
			return nil, fmt.Errorf("task correction revision already exists")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.taskRevisionPath(task.ProjectID, task.ID, candidate.TaskRevision), candidate); err != nil {
			return nil, err
		}
		currentState.Status = "ready"
		currentState.UpdatedAt = now
		if err := hub.WriteJSON(worktree, s.taskStatePath(task.ProjectID, task.ID), currentState); err != nil {
			return nil, err
		}
		return []string{s.taskRevisionPath(task.ProjectID, task.ID, candidate.TaskRevision), s.taskStatePath(task.ProjectID, task.ID)}, nil
	})
	if err != nil {
		return model.TaskRevision{}, OperationResult{}, err
	}
	return candidate, OperationResult{Hub: tx, ProjectID: task.ProjectID, TaskID: task.ID, Status: "revision_created"}, nil
}
