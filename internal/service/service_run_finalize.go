package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) RunFinalize(ctx context.Context, in FinalizeInput) (model.Report, OperationResult, error) {
	if err := requireCanonicalRunID(in.RunID); err != nil {
		return model.Report{}, OperationResult{}, err
	}
	run, err := s.findRun(ctx, in.RunID)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if err := requireCanonicalRun(run); err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if err := s.ensureRunOwned(run); err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if !operationalActiveRun(run) {
		return model.Report{}, OperationResult{}, fmt.Errorf("run is not active: %s", run.Status)
	}
	task, err := s.findTask(ctx, run.TaskID)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	canonicalCompletionPath, err := gatewayCompletionDestination(s.Config.StateDir, run)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	completionPath, err := gatewayCompletionPath(run, in.CompletionFile)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if completionPath != canonicalCompletionPath {
		return model.Report{}, OperationResult{}, fmt.Errorf("completion file must equal the canonical Run-specific path")
	}
	data, err := fsutil.ReadFileBounded(completionPath, s.Config.MaxReadBytes)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if s.Config.MaxReadBytes > 0 && int64(len(data)) > s.Config.MaxReadBytes {
		return model.Report{}, OperationResult{}, fmt.Errorf("completion exceeds configured output limit")
	}
	completion, err := model.ParseCompletion(data, task)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if completion.RunID != run.ID || completion.TaskSHA256 != run.TaskSHA256 || completion.TaskRevision != run.TaskRevision || completion.TaskRevisionSHA256 != run.TaskRevisionSHA256 || completion.TaskRunNumber != run.TaskRunNumber {
		return model.Report{}, OperationResult{}, fmt.Errorf("completion identity does not match active run")
	}
	if err := model.ValidateTaskHash(task); err != nil || run.TaskSHA256 != task.SHA256 {
		return model.Report{}, OperationResult{}, fmt.Errorf("durable task hash mismatch")
	}
	local, err := s.projectConfig(run.ProjectID)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	gateResults, err := s.ExecuteProjectGates(ctx, run.ProjectID, task.OperationClass, local.Root)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	expectedGates, err := s.ResolveProjectGates(ctx, run.ProjectID, task.OperationClass)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if err := validateProjectGateEvidence(gateResults, expectedGates); err != nil {
		return model.Report{}, OperationResult{}, err
	}
	head, branch, clean, err := s.Git.CurrentHead(ctx, local)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if branch != run.Branch {
		return model.Report{}, OperationResult{}, fmt.Errorf("repository branch does not match task branch")
	}
	if completion.Status == "succeeded" && !clean {
		return model.Report{}, OperationResult{}, fmt.Errorf("successful run must leave clean worktree")
	}
	proof, risks, err := s.durableRepositoryProof(ctx, run, local, head, branch, clean, true)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	now := time.Now().UTC()
	run.Status = completion.Status
	run.FinishedAt = &now
	remainingRisks := append([]string{}, completion.RemainingRisks...)
	for _, risk := range risks {
		addUniqueRisk(&remainingRisks, risk)
	}
	report := canonicalReport(model.Report{SchemaVersion: model.SchemaVersion, TaskID: task.ID, RunID: run.ID, TaskRevision: run.TaskRevision, TaskRevisionSHA256: run.TaskRevisionSHA256, TaskRunNumber: run.TaskRunNumber, ProjectID: run.ProjectID, Status: completion.Status, Summary: completion.Summary, GateResults: completion.GateResults, ServerGateResults: gateResults, AcceptanceCoverage: completion.AcceptanceCoverage, Deviations: completion.Deviations, RemainingRisks: remainingRisks, AgentFeedback: completion.AgentFeedback, Repository: proof, FinishedAt: now})
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return model.Report{}, OperationResult{}, err
		}
	}
	plan, err := s.PlanRead(ctx, task.ProjectID)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	plan.Revision++
	plan.ActiveRunID = ""
	plan.ActiveTaskID = ""
	plan.UpdatedBy = s.Config.GatewayID
	plan.UpdatedAt = now
	tx, err := s.Hub.Transact(ctx, expected, "gateway: finalize run "+run.ID, func(w string) ([]string, error) {
		state := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: taskStateStatusForResult(completion.Status), UpdatedAt: now}
		paths := []string{s.runPath(run.ProjectID, run.ID), s.reportPath(run.ProjectID, run.ID), s.taskStatePath(task.ProjectID, task.ID), s.planPath(task.ProjectID)}
		vals := []any{run, report, state, plan}
		for i, p := range paths {
			if err := hub.WriteJSON(w, p, vals[i]); err != nil {
				return nil, err
			}
		}
		return paths, nil
	})
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	report.HubCommit = tx.After
	return report, OperationResult{
		Hub:       tx,
		ProjectID: run.ProjectID,
		TaskID:    run.TaskID,
		RunID:     run.ID,
		Status:    "TASK_FINALIZED",
	}, nil
}

func (s *Service) RunReport(ctx context.Context, id string) (model.Report, error) {
	run, err := s.findRun(ctx, id)
	if err != nil {
		return model.Report{}, err
	}
	if run.Historical {
		return model.Report{}, fmt.Errorf("workflow-v1 run report is history-only")
	}
	var report model.Report
	path := s.reportPath(run.ProjectID, id)
	if err := s.Hub.ReadJSON(ctx, path, &report); err != nil {
		return model.Report{}, err
	}
	task, err := s.readTaskForRun(ctx, run)
	if err != nil {
		return model.Report{}, err
	}
	if err := model.ValidateReport(report, task, run, s.Config.MaxListItems); err != nil {
		return model.Report{}, err
	}
	local, err := s.projectConfig(run.ProjectID)
	if err != nil {
		return model.Report{}, err
	}
	if err := s.Git.Refresh(ctx, local); err != nil {
		return model.Report{}, err
	}
	if err := s.validateCanonicalReportProof(ctx, report, run, local); err != nil {
		return model.Report{}, err
	}
	if len(report.ServerGateResults) > 0 {
		expectedGates, err := s.ResolveProjectGates(ctx, run.ProjectID, task.OperationClass)
		if err != nil {
			return model.Report{}, err
		}
		if err := validateProjectGateEvidence(report.ServerGateResults, expectedGates); err != nil {
			return model.Report{}, err
		}
	}
	if run.Status != report.Status {
		return model.Report{}, fmt.Errorf("report status does not match run")
	}
	state, err := s.taskState(ctx, task)
	if err != nil {
		return model.Report{}, err
	}
	if report.Status == "succeeded" {
		switch state.Status {
		case "completed", "merge_ready", "deferred", "merged":
		default:
			return model.Report{}, fmt.Errorf("report status does not match task state")
		}
	} else if state.Status != "ready" {
		return model.Report{}, fmt.Errorf("report status does not match task state")
	}
	commit, err := s.Hub.LastChange(ctx, path)
	if err != nil {
		return model.Report{}, err
	}
	report.HubCommit = commit
	return canonicalReport(report), nil
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
