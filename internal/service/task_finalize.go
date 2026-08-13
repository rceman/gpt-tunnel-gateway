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

func (s *Service) finalizeTaskByIdentity(ctx context.Context, in TaskFinalizeInput) (TrainV2AttemptFinalizeResult, error) {
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
	startHead, branch, _, err := s.Git.CurrentHead(ctx, project)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	startPath := hub.ProtocolRoot + "/projects/" + in.ProjectID + "/train-v2-starts/" + current.Train.ID + ".json"
	var start model.TrainV2StartRecord
	if err := s.Hub.ReadJSON(ctx, startPath, &start); err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	if branch != start.LaneBranch || startHead != current.Attempt.StartHead {
		return TrainV2AttemptFinalizeResult{}, fmt.Errorf("Task worktree identity changed before finalization")
	}
	gates, err := s.ResolveProjectGates(ctx, in.ProjectID, "implementation")
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	serverGates, err := s.executeProjectGatesWithProjectCommands(ctx, in.ProjectID, project.Root, gates, "task")
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
	checkpoint, err := s.Git.CommitCandidate(ctx, project, "Task checkpoint "+in.TaskID)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, fmt.Errorf("create verified Task checkpoint: %w", err)
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
	report := model.TrainV2AttemptReport{
		SchemaVersion: 1, TrainID: current.Train.ID, TaskID: in.TaskID, ItemPosition: current.Item.Position,
		AttemptNumber: current.Attempt.Number, ProjectID: in.ProjectID, Status: "succeeded", Summary: taskFinalizeSummary,
		GateResults: []model.CompletionGateResult{}, ServerGateResults: serverGates, AcceptanceCoverage: []string{},
		Deviations: []string{}, RemainingRisks: []string{},
		Repository: model.RepositoryProof{Branch: finalBranch, Head: finalHead, WorktreeClean: true, BaseAncestor: true, Commits: []string{finalHead}, ChangedFiles: changed, DiffScope: "attempt-start..task-checkpoint"},
		FinishedAt: finished,
	}
	if err := model.ValidateTrainV2AttemptReport(report); err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	tx, err := s.Hub.Transact(ctx, expected, "gateway: finalize Task checkpoint", func(worktree string) ([]string, error) {
		var latest model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(in.ProjectID, current.Train.ID), &latest); err != nil {
			return nil, err
		}
		if latest.Revision != current.Train.Revision || current.Item.Position >= len(latest.Items) {
			return nil, fmt.Errorf("Train changed before Task checkpoint publication")
		}
		item := latest.Items[current.Item.Position]
		if item.TaskID != in.TaskID || item.ActiveAttemptNumber != current.Attempt.Number || item.Attempts[current.Attempt.Number-1].Status != model.TrainV2AttemptRunning {
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
		hasQueued := false
		for _, next := range latest.Items {
			if next.Status == model.TrainV2ItemQueued {
				hasQueued = true
				break
			}
		}
		if !hasQueued {
			latest.Status = model.TrainV2ReadyForIntegration
		}
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
	return s.advanceFinalizedTask(ctx, in.ProjectID, in.TaskID, result)
}

func (s *Service) reuseFinalizedTask(ctx context.Context, projectID, taskID string) (TrainV2AttemptFinalizeResult, bool, error) {
	trains, err := s.TrainV2List(ctx, TrainV2ListInput{
		ProjectID: projectID,
		Limit:     model.MaxTrainV2Items,
	})
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, false, err
	}
	for _, train := range trains.Trains {
		for _, item := range train.Items {
			if item.TaskID != taskID || item.Status != model.TrainV2ItemFinalized || item.SuccessfulAttemptNumber == 0 || item.Proof == nil {
				continue
			}
			path := trainV2AttemptReportPath(projectID, train.ID, item.Position, item.SuccessfulAttemptNumber)
			var report model.TrainV2AttemptReport
			if err := s.Hub.ReadJSON(ctx, path, &report); err != nil {
				return TrainV2AttemptFinalizeResult{}, false, err
			}
			if err := model.ValidateTrainV2AttemptReport(report); err != nil || report.TaskID != taskID || report.Repository.Head != item.Proof.CheckpointHead {
				return TrainV2AttemptFinalizeResult{}, false, fmt.Errorf("stored Task checkpoint proof is invalid")
			}
			return TrainV2AttemptFinalizeResult{Report: report}, true, nil
		}
	}
	return TrainV2AttemptFinalizeResult{}, false, nil
}

func (s *Service) advanceFinalizedTask(ctx context.Context, projectID, taskID string, result TrainV2AttemptFinalizeResult) (TrainV2AttemptFinalizeResult, error) {
	train, err := s.TrainV2Read(ctx, projectID, result.Report.TrainID)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	for _, item := range train.Items {
		if item.TaskID == taskID {
			if item.Position+1 < len(train.Items) && train.Items[item.Position+1].Status == model.TrainV2ItemQueued {
				advanced, err := s.TrainV2Advance(ctx, TrainV2AdvanceInput{
					ProjectID: projectID,
					TrainID:   train.ID,
				})
				if err != nil {
					return TrainV2AttemptFinalizeResult{}, err
				}
				result.NextTaskID = advanced.Record.CurrentTaskID
			}
			break
		}
	}
	return result, nil
}
