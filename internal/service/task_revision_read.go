package service

import (
	"context"
	"fmt"
	"sort"

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
