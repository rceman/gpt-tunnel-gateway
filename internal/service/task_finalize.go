package service

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

const taskFinalizeSummary = "Gateway verified the Task worktree, created the checkpoint, and finalized the TrainItem Attempt."

const (
	taskCheckpointAuthor      = "GPT Tunnel Gateway"
	taskCheckpointAuthorEmail = "gpt-tunnel-gateway@localhost"
)

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
	orphan := startHead != current.Attempt.StartHead
	if orphan {
		if !clean {
			return TrainV2AttemptFinalizeResult{}, fmt.Errorf("orphan Task checkpoint worktree is not clean")
		}
		if err := s.validateOrphanTaskCheckpoint(ctx, project, in.TaskID, current.Attempt.StartHead, startHead); err != nil {
			return TrainV2AttemptFinalizeResult{}, err
		}
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

func (s *Service) validateOrphanTaskCheckpoint(ctx context.Context, project config.ProjectConfig, taskID, startHead, checkpoint string) error {
	if model.ValidateCommitSHA(startHead) != nil || model.ValidateCommitSHA(checkpoint) != nil {
		return fmt.Errorf("orphan Task checkpoint identity is invalid")
	}
	commits, err := s.Git.Log(ctx, project, checkpoint, 1)
	if err != nil || len(commits) != 1 {
		return fmt.Errorf("orphan Task checkpoint provenance is unavailable")
	}
	commit := commits[0]
	if commit.SHA != checkpoint || len(commit.Parents) != 1 || commit.Parents[0] != startHead || commit.Subject != "Task checkpoint "+taskID || commit.AuthorName != taskCheckpointAuthor || commit.AuthorEmail != taskCheckpointAuthorEmail {
		return fmt.Errorf("orphan Task checkpoint provenance does not match the active Attempt")
	}
	tree, err := s.Git.TreeID(ctx, project)
	if err != nil {
		return err
	}
	content, err := s.Git.WorktreeContentID(ctx, project)
	if err != nil || tree != content {
		return fmt.Errorf("orphan Task checkpoint tree does not match the prepared worktree")
	}
	return nil
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
			if item.TaskID != taskID || item.Status != model.TrainV2ItemFinalized || item.SuccessfulAttemptNumber == 0 {
				continue
			}
			path := trainV2AttemptReportPath(projectID, train.ID, item.Position, item.SuccessfulAttemptNumber)
			var report model.TrainV2AttemptReport
			if err := s.Hub.ReadJSON(ctx, path, &report); err != nil {
				return TrainV2AttemptFinalizeResult{}, false, err
			}
			if item.Proof == nil {
				recovered, err := s.recoverFinalizedTaskProof(ctx, projectID, train, item, report, path)
				if err != nil {
					return TrainV2AttemptFinalizeResult{}, false, err
				}
				return TrainV2AttemptFinalizeResult{Report: recovered}, true, nil
			}
			if err := validateStoredTrainItemProof(report, train, item, path, taskID); err != nil {
				return TrainV2AttemptFinalizeResult{}, false, fmt.Errorf("stored Task checkpoint proof is invalid")
			}
			return TrainV2AttemptFinalizeResult{Report: report}, true, nil
		}
	}
	return TrainV2AttemptFinalizeResult{}, false, nil
}

func validateProofRecoveryReport(report model.TrainV2AttemptReport, projectID string, train model.TrainV2, item model.TrainV2Item, path string) error {
	if err := model.ValidateTrainV2AttemptReport(report); err != nil {
		return err
	}
	if report.ProjectID != projectID || report.TrainID != train.ID || report.TaskID != item.TaskID || report.ItemPosition != item.Position || report.AttemptNumber != item.SuccessfulAttemptNumber || report.Status != "succeeded" {
		return fmt.Errorf("Attempt report identity or status mismatch")
	}
	if item.SuccessfulAttemptNumber == 0 || item.SuccessfulAttemptNumber > uint64(len(item.Attempts)) || item.Attempts[item.SuccessfulAttemptNumber-1].Status != model.TrainV2AttemptSucceeded {
		return fmt.Errorf("successful Attempt is not the exact finalized item Attempt")
	}
	if report.Repository.Branch == "" || model.ValidateBranch(report.Repository.Branch) != nil || !report.Repository.WorktreeClean || !report.Repository.BaseAncestor || report.Repository.DiffScope == "" || model.ValidateCommitSHA(report.Repository.Head) != nil {
		return fmt.Errorf("Attempt report repository proof is invalid")
	}
	if len(report.ServerGateResults) == 0 {
		return fmt.Errorf("Attempt report is missing server-owned gate evidence")
	}
	if err := model.ValidateServerGateEvidence(report.ServerGateResults); err != nil {
		return err
	}
	for _, gate := range report.ServerGateResults {
		if gate.ExitCode != 0 {
			return fmt.Errorf("Attempt report contains failed server gate %s", gate.ID)
		}
	}
	if path == "" {
		return fmt.Errorf("Attempt report path is required")
	}
	return nil
}

func validateStoredTrainItemProof(report model.TrainV2AttemptReport, train model.TrainV2, item model.TrainV2Item, path, taskID string) error {
	if err := validateProofRecoveryReport(report, train.ProjectID, train, item, path); err != nil || report.TaskID != taskID || item.Proof == nil {
		return fmt.Errorf("invalid stored Attempt proof report")
	}
	if item.Proof.ReportID != path || item.Proof.CheckpointHead != report.Repository.Head || item.Proof.ImplementationSHA != report.Repository.Head || !reflect.DeepEqual(item.Proof.GateResults, report.ServerGateResults) {
		return fmt.Errorf("stored implementation proof does not match Attempt report")
	}
	return nil
}

func (s *Service) recoverFinalizedTaskProof(ctx context.Context, projectID string, train model.TrainV2, item model.TrainV2Item, report model.TrainV2AttemptReport, reportPath string) (model.TrainV2AttemptReport, error) {
	if err := validateProofRecoveryReport(report, projectID, train, item, reportPath); err != nil {
		return model.TrainV2AttemptReport{}, fmt.Errorf("proof recovery rejected: %w", err)
	}
	project, err := s.projectConfig(projectID)
	if err != nil {
		return model.TrainV2AttemptReport{}, err
	}
	startPath := hub.ProtocolRoot + "/projects/" + projectID + "/train-v2-starts/" + train.ID + ".json"
	var start model.TrainV2StartRecord
	if err := s.Hub.ReadJSON(ctx, startPath, &start); err != nil {
		return model.TrainV2AttemptReport{}, fmt.Errorf("proof recovery start record: %w", err)
	}
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, projectID, train.ID)
	if err != nil {
		return model.TrainV2AttemptReport{}, fmt.Errorf("proof recovery runtime: %w", err)
	}
	project.Root = runtime.WorktreePath
	head, branch, clean, err := s.Git.CurrentHead(ctx, project)
	if err != nil {
		return model.TrainV2AttemptReport{}, err
	}
	if !clean || branch != start.LaneBranch || branch != report.Repository.Branch {
		return model.TrainV2AttemptReport{}, fmt.Errorf("proof recovery lane identity is invalid")
	}
	ancestor, err := s.Git.IsAncestor(ctx, project.Root, report.Repository.Head, head)
	if err != nil {
		return model.TrainV2AttemptReport{}, fmt.Errorf("proof recovery checkpoint lookup: %w", err)
	}
	if !ancestor {
		return model.TrainV2AttemptReport{}, fmt.Errorf("proof recovery checkpoint is not an ancestor of the current lane head")
	}
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return model.TrainV2AttemptReport{}, err
	}
	proof := model.TrainV2ImplementationProof{CheckpointHead: report.Repository.Head, ImplementationSHA: report.Repository.Head, ReportID: reportPath, GateResults: append([]model.CompletionGateResult{}, report.ServerGateResults...), RecordedAt: report.FinishedAt}
	_, err = s.Hub.Transact(ctx, expected, "gateway: recover Train Attempt implementation proof", func(worktree string) ([]string, error) {
		var latest model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(projectID, train.ID), &latest); err != nil {
			return nil, err
		}
		if latest.Revision != train.Revision || item.Position >= len(latest.Items) {
			return nil, fmt.Errorf("Train changed before proof recovery")
		}
		latestItem := latest.Items[item.Position]
		if latestItem.TaskID != item.TaskID || latestItem.Status != model.TrainV2ItemFinalized || latestItem.SuccessfulAttemptNumber != item.SuccessfulAttemptNumber || latestItem.Proof != nil {
			return nil, fmt.Errorf("Train Attempt proof recovery identity changed")
		}
		var latestReport model.TrainV2AttemptReport
		if err := readWorktreeJSON(worktree, reportPath, &latestReport); err != nil {
			return nil, err
		}
		if err := validateProofRecoveryReport(latestReport, projectID, latest, latestItem, reportPath); err != nil || !reflect.DeepEqual(latestReport, report) {
			return nil, fmt.Errorf("Attempt report changed before proof recovery")
		}
		latestItem.Proof = &proof
		latest.Items[item.Position] = latestItem
		latest.Revision++
		latest.UpdatedAt = time.Now().UTC()
		if err := model.ValidateTrainV2(latest); err != nil {
			return nil, err
		}
		trainPath := s.trainV2Path(projectID, train.ID)
		if err := hub.WriteJSON(worktree, trainPath, latest); err != nil {
			return nil, err
		}
		return []string{trainPath}, nil
	})
	if err != nil {
		return model.TrainV2AttemptReport{}, err
	}
	return report, nil
}

func (s *Service) advanceFinalizedTask(ctx context.Context, projectID, taskID string, result TrainV2AttemptFinalizeResult) (TrainV2AttemptFinalizeResult, error) {
	lock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "train-"+result.Report.TrainID)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	defer lock.Release()
	return s.advanceFinalizedTaskLocked(ctx, projectID, taskID, result)
}

func (s *Service) advanceFinalizedTaskLocked(ctx context.Context, projectID, taskID string, result TrainV2AttemptFinalizeResult) (TrainV2AttemptFinalizeResult, error) {
	train, err := s.TrainV2Read(ctx, projectID, result.Report.TrainID)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	for _, item := range train.Items {
		if item.TaskID == taskID {
			if item.Position+1 < len(train.Items) && train.Items[item.Position+1].Status == model.TrainV2ItemQueued {
				advanced, err := s.advanceTrainV2Locked(ctx, TrainV2AdvanceInput{
					ProjectID: projectID,
					TrainID:   train.ID,
				}, true)
				if err != nil {
					return TrainV2AttemptFinalizeResult{}, err
				}
				result.NextTaskID = advanced.Record.CurrentTaskID
				return result, nil
			}
			closed, err := s.closeoutTrain(ctx, projectID, train)
			if err != nil {
				return TrainV2AttemptFinalizeResult{}, err
			}
			result.TrainStatus = closed.Status
			break
		}
	}
	return result, nil
}

func (s *Service) closeoutTrain(ctx context.Context, projectID string, train model.TrainV2) (model.TrainV2, error) {
	project, err := s.projectConfig(projectID)
	if err != nil {
		return model.TrainV2{}, err
	}
	startPath := hub.ProtocolRoot + "/projects/" + projectID + "/train-v2-starts/" + train.ID + ".json"
	var start model.TrainV2StartRecord
	if err := s.Hub.ReadJSON(ctx, startPath, &start); err != nil {
		return model.TrainV2{}, err
	}
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, projectID, train.ID)
	if err != nil {
		return model.TrainV2{}, fmt.Errorf("read Train closeout runtime: %w", err)
	}
	project.Root = runtime.WorktreePath
	head, branch, clean, err := s.Git.CurrentHead(ctx, project)
	if err != nil {
		return model.TrainV2{}, err
	}
	if !clean || branch != start.LaneBranch {
		return model.TrainV2{}, fmt.Errorf("Train closeout requires a clean bound worktree")
	}
	for _, item := range train.Items {
		if item.Status != model.TrainV2ItemFinalized || item.Proof != nil {
			continue
		}
		if item.SuccessfulAttemptNumber == 0 || item.SuccessfulAttemptNumber > uint64(len(item.Attempts)) {
			return model.TrainV2{}, fmt.Errorf("Train closeout cannot recover an item without a successful Attempt")
		}
		reportPath := trainV2AttemptReportPath(projectID, train.ID, item.Position, item.SuccessfulAttemptNumber)
		var report model.TrainV2AttemptReport
		if err := s.Hub.ReadJSON(ctx, reportPath, &report); err != nil {
			return model.TrainV2{}, err
		}
		if _, err := s.recoverFinalizedTaskProof(ctx, projectID, train, item, report, reportPath); err != nil {
			return model.TrainV2{}, err
		}
		train, err = s.TrainV2Read(ctx, projectID, train.ID)
		if err != nil {
			return model.TrainV2{}, err
		}
	}
	if train.FullProof != nil {
		if train.FullProof.CandidateHead != head {
			return model.TrainV2{}, fmt.Errorf("Train closeout proof invalidated by exact-head drift")
		}
		for _, item := range train.Items {
			if item.Status != model.TrainV2ItemFinalized || item.SuccessfulAttemptNumber == 0 || item.Proof == nil || item.SuccessfulAttemptNumber > uint64(len(item.Attempts)) {
				return model.TrainV2{}, fmt.Errorf("Train closeout requires every finalized item to have proof")
			}
			reportPath := trainV2AttemptReportPath(projectID, train.ID, item.Position, item.SuccessfulAttemptNumber)
			var report model.TrainV2AttemptReport
			if err := s.Hub.ReadJSON(ctx, reportPath, &report); err != nil {
				return model.TrainV2{}, err
			}
			if err := validateStoredTrainItemProof(report, train, item, reportPath, item.TaskID); err != nil {
				return model.TrainV2{}, err
			}
		}
		return train, nil
	}
	for i, item := range train.Items {
		if item.Status != model.TrainV2ItemFinalized || item.SuccessfulAttemptNumber == 0 || item.SuccessfulAttemptNumber > uint64(len(item.Attempts)) || item.Attempts[item.SuccessfulAttemptNumber-1].Status != model.TrainV2AttemptSucceeded {
			return model.TrainV2{}, fmt.Errorf("Train closeout requires every item to have a successful Attempt")
		}
		if item.Proof == nil {
			return model.TrainV2{}, fmt.Errorf("Train closeout requires every finalized item to have proof")
		}
		reportPath := trainV2AttemptReportPath(projectID, train.ID, item.Position, item.SuccessfulAttemptNumber)
		var report model.TrainV2AttemptReport
		if err := s.Hub.ReadJSON(ctx, reportPath, &report); err != nil {
			return model.TrainV2{}, err
		}
		if err := validateStoredTrainItemProof(report, train, item, reportPath, item.TaskID); err != nil {
			return model.TrainV2{}, err
		}
		if i == len(train.Items)-1 && item.Proof.ImplementationSHA != head {
			return model.TrainV2{}, fmt.Errorf("Train closeout requires the final item proof to compose the exact lane head")
		}
	}
	treeBefore, err := s.Git.TreeID(ctx, project)
	if err != nil {
		return model.TrainV2{}, err
	}
	gates, err := s.ResolveProjectGates(ctx, projectID, "integration")
	if err != nil {
		return model.TrainV2{}, err
	}
	results, err := s.executeProjectGatesWithProjectCommands(ctx, projectID, project.Root, gates, "train")
	if err != nil {
		return model.TrainV2{}, fmt.Errorf("Train closeout gates failed; repair the Train lane and retry: %w", err)
	}
	for _, gate := range results {
		if gate.ExitCode != 0 {
			return model.TrainV2{}, fmt.Errorf("Train closeout gate %s failed; repair the Train lane and retry", gate.ID)
		}
	}
	postHead, postBranch, postClean, err := s.Git.CurrentHead(ctx, project)
	if err != nil {
		return model.TrainV2{}, err
	}
	treeAfter, err := s.Git.TreeID(ctx, project)
	if err != nil {
		return model.TrainV2{}, err
	}
	if postHead != head || postBranch != branch || !postClean || treeAfter != treeBefore {
		return model.TrainV2{}, fmt.Errorf("Train closeout head or tree drifted during gates")
	}
	now := time.Now().UTC()
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return model.TrainV2{}, err
	}
	updated := train
	updated.FullProof = &model.TrainV2FullProof{CandidateHead: head, GateResults: append([]model.CompletionGateResult{}, results...), RecordedAt: now}
	updated.Status = model.TrainV2ReadyForIntegration
	updated.Revision++
	updated.UpdatedAt = now
	if err := model.ValidateTrainV2(updated); err != nil {
		return model.TrainV2{}, err
	}
	if _, err := s.Hub.Transact(ctx, expected, "gateway: closeout Train v2", func(worktree string) ([]string, error) {
		path := s.trainV2Path(projectID, train.ID)
		var latest model.TrainV2
		if err := readWorktreeJSON(worktree, path, &latest); err != nil {
			return nil, err
		}
		if latest.Revision != train.Revision || latest.FullProof != nil || latest.Status != model.TrainV2Running {
			return nil, fmt.Errorf("Train changed before closeout proof publication")
		}
		if err := hub.WriteJSON(worktree, path, updated); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		return model.TrainV2{}, err
	}
	return updated, nil
}
