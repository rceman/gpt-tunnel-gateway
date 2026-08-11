package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

var snapshotPathRE = regexp.MustCompile(`/[^ ]+`)

func snapshotDetail(err error) string {
	if err == nil {
		return ""
	}
	s := strings.Join(strings.Fields(err.Error()), " ")
	if strings.Contains(s, "git ") {
		s = "Git component operation failed"
	}
	s = snapshotPathRE.ReplaceAllString(s, "<path>")
	if len(s) > 240 {
		s = s[:240]
	}
	return s
}

func (s *Service) RunReviewSnapshot(ctx context.Context, id string) (model.ReviewSnapshot, error) {
	run, err := s.findRun(ctx, id)
	if err != nil {
		return model.ReviewSnapshot{}, err
	}
	if run.Historical {
		return model.ReviewSnapshot{}, fmt.Errorf("workflow-v1 run review snapshot is history-only")
	}
	task, taskErr := s.readTaskForRun(ctx, run)
	state, stateErr := s.taskState(ctx, task)
	if stateErr != nil {
		state = model.TaskState{TaskID: task.ID, TaskSHA256: task.SHA256, Status: "unknown"}
	}
	snapshot := model.ReviewSnapshot{SchemaVersion: 1,
		Run:    model.ReviewSnapshotRun{ID: run.ID, TaskID: run.TaskID, ProjectID: run.ProjectID, Status: run.Status, Branch: run.Branch, BaseRevision: run.BaseRevision, CreatedAt: run.CreatedAt, DispatchedAt: run.DispatchedAt, FinishedAt: run.FinishedAt},
		Task:   model.ReviewSnapshotTask{ID: task.ID, SHA256: task.SHA256, Title: task.Title, Objective: task.Objective, Branch: task.Branch, BaseRevision: task.BaseRevision, AcceptanceCriteria: snapshotStrings(task.AcceptanceCriteria), Constraints: snapshotStrings(task.Constraints), RequiredGates: snapshotStrings(task.RequiredGates), CreatedBy: task.CreatedBy, CreatedAt: task.CreatedAt, TaskStateStatus: state.Status},
		Checks: []model.ReviewSnapshotCheck{},
	}
	terminal := !operationalActiveRun(run)
	project, err := s.projectConfig(run.ProjectID)
	if err != nil {
		return model.ReviewSnapshot{}, err
	}
	repo := model.ReviewSnapshotRepo{RefreshAttempted: true, DefaultBranch: project.DefaultBranch, TaskBranch: run.Branch, ChangedFiles: []string{}}
	refreshErr := s.Git.Refresh(ctx, project)
	var report model.ReviewSnapshotReport
	var reportErr error
	if refreshErr != nil {
		reportErr = refreshErr
	} else {
		report, reportErr = s.readSnapshotReport(ctx, run, task, project)
	}
	evidence, evidenceErr := snapshotEvidenceFromReport(report, reportErr)
	if terminal && reportErr == nil {
		wantState := "ready"
		if report.Status == "succeeded" {
			wantState = "completed"
		}
		if state.Status != wantState {
			reportErr = fmt.Errorf("report status does not match task state")
		}
	}
	if !terminal {
		report = model.ReviewSnapshotReport{Available: false}
		evidence = model.ReviewSnapshotEvidence{Available: false}
	}
	if reportErr != nil && terminal {
		report.Error = snapshotDetail(reportErr)
	}
	if evidenceErr != nil && terminal {
		evidence.Error = snapshotDetail(evidenceErr)
	}
	snapshot.Report, snapshot.Evidence = report, evidence

	if refreshErr != nil {
		repo.RefreshError = snapshotDetail(refreshErr)
	} else {
		repo.RefreshSucceeded = true
		s.fillSnapshotRepository(ctx, project, run, snapshot.Evidence, snapshot.Report, &repo)
	}
	snapshot.Repository = repo
	snapshot.Checks = snapshotChecks(run, task, state, taskErr, stateErr, reportErr, evidenceErr, snapshot.Report, snapshot.Evidence, repo, terminal)
	snapshot.ReviewState = "active"
	snapshot.NextAction = "wait_for_terminal"
	if terminal {
		snapshot.ReviewState = "reviewable"
		snapshot.NextAction = "perform_static_review"
		for _, check := range snapshot.Checks {
			if check.Severity == "critical" && check.Status == "fail" {
				snapshot.ReviewState = "blocked"
				snapshot.NextAction = "resolve_structural_failure"
				break
			}
		}
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return model.ReviewSnapshot{}, fmt.Errorf("review snapshot encoding failed")
	}
	if s.Config.MaxReadBytes > 0 && int64(len(data)) > s.Config.MaxReadBytes {
		return model.ReviewSnapshot{}, fmt.Errorf("review snapshot exceeds configured output limit")
	}
	return snapshot, nil
}

func (s *Service) readTaskForRun(ctx context.Context, run model.Run) (model.Task, error) {
	var task model.Task
	if err := s.Hub.ReadJSON(ctx, s.taskPath(run.ProjectID, run.TaskID), &task); err != nil {
		return task, err
	}
	if task.ID != run.TaskID || task.ProjectID != run.ProjectID || task.SHA256 != run.TaskSHA256 {
		return task, fmt.Errorf("task identity mismatch")
	}
	return task, nil
}

func (s *Service) readSnapshotReport(ctx context.Context, run model.Run, task model.Task, project config.ProjectConfig) (model.ReviewSnapshotReport, error) {
	var report model.Report
	if err := s.Hub.ReadJSON(ctx, s.reportPath(run.ProjectID, run.ID), &report); err != nil {
		return model.ReviewSnapshotReport{}, err
	}
	if report.SchemaVersion != model.SchemaVersion || report.TaskID != task.ID || report.RunID != run.ID || report.ProjectID != run.ProjectID || report.Status == "" || report.FinishedAt.IsZero() {
		return model.ReviewSnapshotReport{}, fmt.Errorf("report identity or completeness mismatch")
	}
	if err := model.ValidateReport(report, task, run, s.Config.MaxListItems); err != nil {
		return model.ReviewSnapshotReport{}, err
	}
	if err := s.validateCanonicalReportProof(ctx, report, run, project); err != nil {
		return model.ReviewSnapshotReport{}, err
	}
	if run.Status != report.Status {
		return model.ReviewSnapshotReport{}, fmt.Errorf("report status does not match run")
	}
	commit, err := s.Hub.LastChange(ctx, s.reportPath(run.ProjectID, run.ID))
	if err != nil {
		return model.ReviewSnapshotReport{}, fmt.Errorf("report hub history unavailable")
	}
	clean := report.Repository.WorktreeClean
	return model.ReviewSnapshotReport{Available: true, Status: report.Status, Summary: report.Summary, RepositoryHead: report.Repository.Head, RepositoryBranch: report.Repository.Branch, RepositoryClean: &clean, Commits: append([]string{}, report.Repository.Commits...), ChangedFiles: append([]string{}, report.Repository.ChangedFiles...), GateResults: append([]model.CompletionGateResult{}, report.GateResults...), ServerGateResults: append([]model.CompletionGateResult{}, report.ServerGateResults...), AcceptanceCoverage: append([]string{}, report.AcceptanceCoverage...), Deviations: append([]string{}, report.Deviations...), RemainingRisks: append([]string{}, report.RemainingRisks...), FinishedAt: &report.FinishedAt, HubCommit: commit}, nil
}
