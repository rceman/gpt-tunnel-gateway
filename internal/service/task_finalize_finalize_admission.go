package service

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const taskFinalizeSummary = "Gateway verified the Task worktree, created the checkpoint, and finalized the TrainItem Attempt."
const (
	taskCheckpointAuthor      = "GPT Tunnel Gateway"
	taskCheckpointAuthorEmail = "gpt-tunnel-gateway@localhost"
)

func validateTaskFinalizeSemantics(in TaskFinalizeInput) error {
	if len([]byte(in.Summary)) > 4096 || len(in.AcceptanceCoverage) > 128 || len(in.Deviations) > 128 || len(in.RemainingRisks) > 128 {
		return fmt.Errorf("Task finalize semantic data exceeds bounds")
	}
	for _, values := range [][]string{in.AcceptanceCoverage, in.Deviations, in.RemainingRisks} {
		for _, value := range values {
			if len([]byte(value)) > 1024 {
				return fmt.Errorf("Task finalize semantic value exceeds bounds")
			}
		}
	}
	return nil
}
func (s *Service) finalizeTaskByIdentity(ctx context.Context, in TaskFinalizeInput) (TrainV2AttemptFinalizeResult, error) {
	if err := validateTaskFinalizeSemantics(in); err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	if in.ProjectID == "" {
		task, err := s.TaskAuthoringFind(ctx, in.TaskID)
		if err != nil {
			return TrainV2AttemptFinalizeResult{}, err
		}
		in.ProjectID = task.ProjectID
	}
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	if result, ok, err := s.reuseFinalizedTask(ctx, in.ProjectID, in.TaskID); err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	} else if ok {
		return s.advanceFinalizedTask(ctx, in.ProjectID, in.TaskID, result)
	}
	current, err := s.taskAttempt(ctx, in.ProjectID, in.TaskID)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	project, err := s.projectConfig(in.ProjectID)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	project.Root = current.Runtime.WorktreePath
	lock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "train-"+current.Train.ID)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	defer lock.Release()
	startHead, branch, clean, err := s.Git.CurrentHead(ctx, project)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	startPath := hub.ProtocolRoot + "/projects/" + in.ProjectID + "/train-v2-starts/" + current.Train.ID + ".json"
	var start model.TrainV2StartRecord
	if err := s.Hub.ReadJSON(ctx, startPath, &start); err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	if branch != start.LaneBranch {
		return TrainV2AttemptFinalizeResult{}, fmt.Errorf("Task worktree identity changed before finalization")
	}
	if s.formatExecutor == nil {
		return TrainV2AttemptFinalizeResult{}, fmt.Errorf("canonical formatter is not configured")
	}
	_, _, clean, err = s.Git.CurrentHead(ctx, project)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	orphan := startHead != current.Attempt.StartHead
	if orphan {
		if !clean {
			return TrainV2AttemptFinalizeResult{}, fmt.Errorf("orphan Task checkpoint worktree is not clean")
		}
		if err := s.validateOrphanTaskCheckpoint(ctx, project, in.TaskID, current.Attempt.StartHead, startHead); err != nil {
			return TrainV2AttemptFinalizeResult{}, err
		}
	}
	changedBeforeGates, err := s.taskFinalizeChangedFiles(ctx, project.Root, current.Attempt.StartHead, startHead)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	testScope := resolveFinalizationTestScope(ctx, "implementation", project.Root, changedBeforeGates)
	gates, err := s.ResolveProjectGates(ctx, in.ProjectID, "implementation")
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	serverGates, err := s.executeTaskFinalizeGatesWithSnapshot(ctx, in.ProjectID, project, gates, changedBeforeGates, testScope)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, fmt.Errorf("Task finalize gates failed; repair the candidate worktree and retry: %w", err)
	}
	for _, gate := range serverGates {
		if gate.ExitCode != 0 {
			return TrainV2AttemptFinalizeResult{}, fmt.Errorf("Task finalize gate %s failed; repair the candidate worktree and retry", gate.ID)
		}
	}
	postGateHead, postGateBranch, _, err := s.Git.CurrentHead(ctx, project)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	if postGateHead != startHead || postGateBranch != branch {
		return TrainV2AttemptFinalizeResult{}, fmt.Errorf("Task worktree/head drifted during finalization gates")
	}
	candidateTree, err := s.Git.WorktreeContentID(ctx, project)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	checkpoint := startHead
	if !orphan {
		checkpoint, err = s.Git.CommitCandidate(ctx, project, "Task checkpoint "+in.TaskID)
		if err != nil {
			return TrainV2AttemptFinalizeResult{}, fmt.Errorf("create verified Task checkpoint: %w", err)
		}
	}
	finalHead, finalBranch, clean, err := s.Git.CurrentHead(ctx, project)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	if !clean || finalBranch != branch {
		return TrainV2AttemptFinalizeResult{}, fmt.Errorf("Task checkpoint worktree is not clean and bound to its branch")
	}
	finalTree, err := s.Git.TreeID(ctx, project)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	if finalHead != checkpoint || finalTree != candidateTree {
		return TrainV2AttemptFinalizeResult{}, fmt.Errorf("Task checkpoint tree does not equal verified candidate tree")
	}
	changed, err := s.Git.ChangedFiles(ctx, project.Root, current.Attempt.StartHead, checkpoint)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	finished := time.Now().UTC()
	reportPath := trainV2AttemptReportPath(in.ProjectID, current.Train.ID, current.Item.Position, current.Attempt.Number)
	summary := in.Summary
	if summary == "" {
		summary = taskFinalizeSummary
	}
	report := model.TrainV2AttemptReport{
		SchemaVersion: 1, TrainID: current.Train.ID, TaskID: in.TaskID, ItemPosition: current.Item.Position,
		AttemptNumber: current.Attempt.Number, ProjectID: in.ProjectID, Status: "succeeded", Summary: summary,
		GateResults: []model.CompletionGateResult{}, ServerGateResults: serverGates, AcceptanceCoverage: append([]string{}, in.AcceptanceCoverage...),
		Deviations: append([]string{}, in.Deviations...), RemainingRisks: append([]string{}, in.RemainingRisks...),
		Repository: model.RepositoryProof{Branch: finalBranch, Head: finalHead, WorktreeClean: true, BaseAncestor: true, Commits: []string{finalHead}, ChangedFiles: changed, DiffScope: "attempt-start..task-checkpoint"},
		FinishedAt: finished,
	}
	if err := model.ValidateTrainV2AttemptReport(report); err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		var err error
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return TrainV2AttemptFinalizeResult{}, err
		}
	}
	tx, err := s.Hub.Transact(ctx, expected, "gateway: finalize Task checkpoint", func(worktree string) ([]string, error) {
		var latest model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(in.ProjectID, current.Train.ID), &latest); err != nil {
			return nil, err
		}
		if current.Item.Position >= len(latest.Items) {
			return nil, fmt.Errorf("Train changed before Task checkpoint publication")
		}
		item := latest.Items[current.Item.Position]
		if item.TaskID != in.TaskID || item.ActiveAttemptNumber != current.Attempt.Number || current.Attempt.Number == 0 || current.Attempt.Number > uint64(len(item.Attempts)) || item.Attempts[current.Attempt.Number-1].Status != model.TrainV2AttemptRunning {
			return nil, fmt.Errorf("Task Attempt changed before checkpoint publication")
		}
		item.Status = model.TrainV2ItemFinalized
		item.ActiveAttemptNumber = 0
		item.SuccessfulAttemptNumber = current.Attempt.Number
		item.Attempts[current.Attempt.Number-1].Status = model.TrainV2AttemptSucceeded
		item.Attempts[current.Attempt.Number-1].FinishedAt = &finished
		item.Attempts[current.Attempt.Number-1].ReportID = reportPath
		item.Proof = &model.TrainV2ImplementationProof{CheckpointHead: finalHead, ImplementationSHA: finalHead, ReportID: reportPath, GateResults: append([]model.CompletionGateResult{}, serverGates...), RecordedAt: finished}
		latest.Items[current.Item.Position] = item
		latest.Revision++
		latest.UpdatedAt = finished
		if err := model.ValidateTrainV2(latest); err != nil {
			return nil, err
		}
		trainPath := s.trainV2Path(in.ProjectID, current.Train.ID)
		if err := hub.WriteJSON(worktree, trainPath, latest); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, reportPath, report); err != nil {
			return nil, err
		}
		return []string{trainPath, reportPath}, nil
	})
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	report.HubCommit = tx.After
	result := TrainV2AttemptFinalizeResult{
		Report: report,
		Hub:    tx,
	}
	return s.advanceFinalizedTaskLocked(ctx, in.ProjectID, in.TaskID, result)
}
