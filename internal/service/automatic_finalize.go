package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// runLegacyCompletionAdvisoryFinalize accepts the old completion file only as
// a bounded source of advisory fields. Its gate results, acceptance coverage
// and claimed status are deliberately discarded before canonical finalization.
func (s *Service) runLegacyCompletionAdvisoryFinalize(ctx context.Context, in FinalizeInput) (model.Report, OperationResult, error) {
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
	canonicalPath, err := gatewayCompletionDestination(s.Config.StateDir, run)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	path, err := gatewayCompletionPath(run, in.CompletionFile)
	if err != nil || path != canonicalPath {
		if err != nil {
			return model.Report{}, OperationResult{}, err
		}
		return model.Report{}, OperationResult{}, fmt.Errorf("completion file must equal the canonical Run-specific path")
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return model.Report{}, OperationResult{}, fmt.Errorf("legacy completion input must be a regular file")
	}
	data, err := os.ReadFile(path)
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
	return s.RunFinalizeAutomatic(ctx, AutomaticFinalizeInput{
		RunID: run.ID, Summary: completion.Summary, Deviations: completion.Deviations,
		RemainingRisks: completion.RemainingRisks, AgentFeedback: completion.AgentFeedback,
		WriteOptions: in.WriteOptions,
	})
}

// AutomaticFinalizeInput is the only Agent-controlled part of canonical
// finalization. Gate and acceptance evidence is always produced by the
// gateway from the immutable Task.
type AutomaticFinalizeInput struct {
	RunID          string
	Summary        string
	Deviations     []string
	RemainingRisks []string
	AgentFeedback  *model.AgentFeedback
	WriteOptions
}

func automaticAcceptanceCoverage(task model.Task, status string) []string {
	if status != "succeeded" {
		return []string{}
	}
	coverage := make([]string, len(task.AcceptanceCriteria))
	for i := range coverage {
		coverage[i] = fmt.Sprintf("AC%d", i+1)
	}
	return coverage
}

func (s *Service) RunFinalizeAutomatic(ctx context.Context, in AutomaticFinalizeInput) (model.Report, OperationResult, error) {
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
	if err := model.ValidateTaskHash(task); err != nil || run.TaskSHA256 != task.SHA256 {
		return model.Report{}, OperationResult{}, fmt.Errorf("durable task hash mismatch")
	}
	local, err := s.projectConfig(run.ProjectID)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	gateResults, status, err := s.runAutomaticGates(ctx, task, local.Root)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	head, branch, clean, err := s.Git.CurrentHead(ctx, local)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if branch != run.Branch {
		return model.Report{}, OperationResult{}, fmt.Errorf("repository branch does not match task branch")
	}
	if status == "succeeded" && !clean {
		return model.Report{}, OperationResult{}, fmt.Errorf("successful run must leave clean worktree")
	}
	proof, risks, err := s.durableRepositoryProof(ctx, run, local, head, branch, clean, true)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	now := time.Now().UTC()
	summary := strings.TrimSpace(in.Summary)
	if summary == "" {
		summary = "Server-owned automatic gate finalization"
	}
	remainingRisks := append([]string{}, in.RemainingRisks...)
	for _, risk := range risks {
		addUniqueRisk(&remainingRisks, risk)
	}
	report := canonicalReport(model.Report{
		SchemaVersion:      model.SchemaVersion,
		TaskID:             task.ID,
		RunID:              run.ID,
		TaskRevision:       run.TaskRevision,
		TaskRevisionSHA256: run.TaskRevisionSHA256,
		TaskRunNumber:      run.TaskRunNumber,
		ProjectID:          run.ProjectID,
		Status:             status,
		Summary:            summary,
		GateResults:        gateResults,
		AcceptanceCoverage: automaticAcceptanceCoverage(task, status),
		Deviations:         append([]string{}, in.Deviations...),
		RemainingRisks:     remainingRisks,
		AgentFeedback:      in.AgentFeedback,
		Repository:         proof,
		FinishedAt:         now,
	})
	if err := model.ValidateReport(report, task, run, s.Config.MaxListItems); err != nil {
		return model.Report{}, OperationResult{}, err
	}
	run.Status = status
	run.FinishedAt = &now
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
	tx, err := s.Hub.Transact(ctx, expected, "gateway: automatic finalize run "+run.ID, func(w string) ([]string, error) {
		state := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: taskStateStatusForResult(status), UpdatedAt: now}
		compactReport := model.CompactReport(report)
		evidence := model.NewGateEvidenceArtifact(report)
		if err := model.ValidateGateEvidenceArtifact(evidence, task, run, s.Config.MaxListItems); err != nil {
			return nil, err
		}
		paths := []string{s.runPath(run.ProjectID, run.ID), s.reportPath(run.ProjectID, run.ID), s.gateEvidencePath(run.ProjectID, run.ID), s.taskStatePath(task.ProjectID, task.ID), s.planPath(task.ProjectID)}
		values := []any{run, compactReport, evidence, state, plan}
		for i, path := range paths {
			if err := hub.WriteJSON(w, path, values[i]); err != nil {
				return nil, err
			}
		}
		return paths, nil
	})
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	report.HubCommit = tx.After
	return report, OperationResult{Hub: tx, ProjectID: run.ProjectID, TaskID: run.TaskID, RunID: run.ID, Status: "TASK_FINALIZED"}, nil
}
