package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
	"github.com/rceman/gpt-tunnel-gateway/internal/watcher"
)

// finalizeTrainV2Run is the Plan-free finalization path. It records the
// implementation proof on the exact TrainItem and writes the Run report in
// one Hub transaction; no TaskState or Plan pointer is read or mutated.
func (s *Service) finalizeTrainV2Run(ctx context.Context, run model.Run, in FinalizeInput) (model.Report, OperationResult, error) {
	authority, err := s.loadTrainV2CompletionAuthority(ctx, run)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if authority.item.Status != model.TrainV2ItemRunning {
		return model.Report{}, OperationResult{}, fmt.Errorf("Train v2 item is not running: %s", authority.item.Status)
	}
	serverOwned := in.CompletionFile == "" && strings.TrimSpace(in.Summary) != ""
	var completion model.Completion
	if !serverOwned {
		data, readErr := fsutil.ReadFileBounded(authority.destination, s.Config.MaxReadBytes)
		if readErr != nil {
			return model.Report{}, OperationResult{}, readErr
		}
		completion, err = model.ParseCompletion(data, authority.completion)
		if err != nil {
			return model.Report{}, OperationResult{}, err
		}
		if completion.RunID != run.ID || completion.TaskSHA256 != run.TaskSHA256 {
			return model.Report{}, OperationResult{}, fmt.Errorf("completion identity does not match Train v2 Run")
		}
	}
	local, err := s.projectConfig(run.ProjectID)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	local.Root = authority.runtime.WorktreePath
	lock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "train-"+run.TrainID)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	defer lock.Release()
	head, branch, clean, err := s.Git.CurrentHead(ctx, local)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if branch != run.Branch || !clean {
		return model.Report{}, OperationResult{}, fmt.Errorf("Train v2 finalization requires the clean Train lane checkout")
	}
	changed, err := s.Git.ChangedFiles(ctx, local.Root, run.BaseRevision, head)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	testScope := resolveFinalizationTestScope(ctx, "implementation", local.Root, changed)
	serverGates, err := s.executeProjectGatesWithTestReuse(ctx, run.ProjectID, local.Root, authority.gates, testScope)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if err := validateProjectGateEvidence(serverGates, authority.gates); err != nil {
		return model.Report{}, OperationResult{}, err
	}
	finalHead, finalBranch, finalClean, err := s.Git.CurrentHead(ctx, local)
	if err != nil || finalHead != head || finalBranch != branch || !finalClean {
		return model.Report{}, OperationResult{}, fmt.Errorf("Train lane changed during gate execution")
	}
	proof, risks, err := s.localTrainRepositoryProof(ctx, run, local.Root, finalBranch, finalHead, finalClean)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if serverOwned {
		completion, err = serverOwnedCompletion(run, authority.completion, in.Summary, in.AgentFeedback, serverGates, risks)
		if err != nil {
			return model.Report{}, OperationResult{}, err
		}
	}
	if completion.Status == "succeeded" && !finalClean {
		return model.Report{}, OperationResult{}, fmt.Errorf("successful Train v2 run must leave a clean lane")
	}
	now := time.Now().UTC()
	updatedRun := run
	updatedRun.Status = completion.Status
	updatedRun.FinishedAt = &now
	remainingRisks := append([]string{}, completion.RemainingRisks...)
	for _, risk := range risks {
		addUniqueRisk(&remainingRisks, risk)
	}
	report := canonicalReport(model.Report{
		SchemaVersion: model.SchemaVersion, TaskID: run.TaskID, RunID: run.ID, ProjectID: run.ProjectID,
		Status: completion.Status, Summary: completion.Summary, GateResults: completion.GateResults,
		ServerGateResults: serverGates, AcceptanceCoverage: completion.AcceptanceCoverage,
		Deviations: completion.Deviations, RemainingRisks: remainingRisks, AgentFeedback: completion.AgentFeedback,
		Repository: proof, FinishedAt: now,
	})
	if err := model.ValidateReport(report, authority.completion, run); err != nil {
		return model.Report{}, OperationResult{}, fmt.Errorf("Train v2 report is invalid: %w", err)
	}
	updatedTrain, err := trainv2.RecordImplementationProof(authority.train, run.TaskID, run.ID, run.AgentID, finalHead, finalHead, run.ID+"-report", serverGates, now)
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	var advance *watcher.AdvancePlan
	if completion.Status == "succeeded" {
		binding, bindErr := watcher.BindTrainRun(authority.train, authority.start, authority.runtime, run)
		if bindErr != nil {
			return model.Report{}, OperationResult{}, bindErr
		}
		planned, ok, planErr := watcher.PlanAutoAdvance(updatedTrain, binding, completion.Status)
		if planErr != nil {
			return model.Report{}, OperationResult{}, planErr
		}
		if ok {
			advance = &planned
		}
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return model.Report{}, OperationResult{}, err
		}
	}
	var nextRun model.Run
	var nextStart model.TrainV2StartRecord
	var tx hub.TransactionResult
	tx, err = s.Hub.Transact(ctx, expected, "gateway: finalize Train v2 Run "+run.ID, func(worktree string) ([]string, error) {
		var currentTrain model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(run.ProjectID, run.TrainID), &currentTrain); err != nil {
			return nil, err
		}
		if currentTrain.Revision != authority.train.Revision {
			return nil, fmt.Errorf("Train v2 changed during finalization")
		}
		var currentRun model.Run
		if err := readWorktreeJSON(worktree, s.runPath(run.ProjectID, run.ID), &currentRun); err != nil {
			return nil, err
		}
		if currentRun.Status != run.Status || currentRun.TrainID != run.TrainID {
			return nil, fmt.Errorf("Train v2 Run changed during finalization")
		}
		paths := []string{s.trainV2Path(run.ProjectID, run.TrainID), s.runPath(run.ProjectID, run.ID), s.reportPath(run.ProjectID, run.ID)}
		if advance != nil {
			var nextTask model.TaskAuthoring
			if err := readWorktreeJSON(worktree, s.taskAuthoringPath(run.ProjectID, advance.Next.TaskID), &nextTask); err != nil {
				return nil, fmt.Errorf("read next Train task: %w", err)
			}
			if nextTask.ProjectID != run.ProjectID || nextTask.Status != model.TaskAuthoringReady || nextTask.Revision != advance.Next.TaskRevision || nextTask.RevisionSHA256 != advance.Next.TaskRevisionSHA256 || nextTask.ReadySeal == nil || nextTask.ReadySeal.Revision != nextTask.Revision || nextTask.ReadySeal.RevisionSHA256 != nextTask.RevisionSHA256 || model.ValidateTaskAuthoring(nextTask) != nil {
				return nil, fmt.Errorf("next Train task is not the exact ready admission")
			}
			nextID, err := trainv2.NextRunID(worktree, s.projectPrefix(run.ProjectID)+"/runs", advance.Next.TaskID)
			if err != nil {
				return nil, err
			}
			nextRun, err = trainv2.BuildNextRun(trainv2.NextRunInput{Current: updatedRun, Next: advance.Next, RunID: nextID, BaseRevision: finalHead, StateDir: s.Config.StateDir, CreatedAt: now})
			if err != nil {
				return nil, err
			}
			nextStart = authority.start
			nextStart.BaseRevision = finalHead
			nextStart.RunID = nextID
			nextStart.CurrentTaskID = advance.Next.TaskID
			nextStart.CurrentTaskRevision = advance.Next.TaskRevision
			nextStart.CurrentTaskRevisionSHA256 = advance.Next.TaskRevisionSHA256
			nextStart.StartedAt = now
			if err := model.ValidateTrainV2StartRecord(nextStart); err != nil {
				return nil, err
			}
			updatedTrain, err = watcher.StartNextItem(updatedTrain, *advance, nextID, finalHead, now)
			if err != nil {
				return nil, err
			}
			paths = append(paths, hub.ProtocolRoot+"/projects/"+run.ProjectID+"/train-v2-starts/"+run.TrainID+".json", s.runPath(run.ProjectID, nextID))
		}
		if err := hub.WriteJSON(worktree, s.trainV2Path(run.ProjectID, run.TrainID), updatedTrain); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.runPath(run.ProjectID, run.ID), updatedRun); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.reportPath(run.ProjectID, run.ID), report); err != nil {
			return nil, err
		}
		if advance != nil {
			if err := hub.WriteJSON(worktree, hub.ProtocolRoot+"/projects/"+run.ProjectID+"/train-v2-starts/"+run.TrainID+".json", nextStart); err != nil {
				return nil, err
			}
			if err := hub.WriteJSON(worktree, s.runPath(run.ProjectID, nextRun.ID), nextRun); err != nil {
				return nil, err
			}
		}
		return paths, nil
	})
	if err != nil {
		return model.Report{}, OperationResult{}, err
	}
	if advance != nil {
		tx, err = s.dispatchTrainV2Continuation(ctx, authority.runtime, nextRun, tx.After, now)
		if err != nil {
			return model.Report{}, OperationResult{}, err
		}
	}
	report.HubCommit = tx.After
	return report, OperationResult{Hub: tx, ProjectID: run.ProjectID, TaskID: run.TaskID, RunID: run.ID, Status: "TASK_FINALIZED"}, nil
}

func (s *Service) trainV2RunReport(ctx context.Context, run model.Run, id string) (model.Report, error) {
	authority, err := s.loadTrainV2HistoricalAuthority(ctx, run)
	if err != nil {
		return model.Report{}, err
	}
	var report model.Report
	if err := s.Hub.ReadJSON(ctx, s.reportPath(run.ProjectID, id), &report); err != nil {
		return model.Report{}, err
	}
	if err := model.ValidateReport(report, authority.completion, run, s.Config.MaxListItems); err != nil {
		return model.Report{}, err
	}
	if err := validateTrainV2HistoricalReportProof(report, run); err != nil {
		return model.Report{}, err
	}
	if len(report.ServerGateResults) > 0 {
		if err := validateProjectGateEvidence(report.ServerGateResults, authority.gates); err != nil {
			return model.Report{}, err
		}
	}
	if run.Status != report.Status {
		return model.Report{}, fmt.Errorf("report status does not match Train v2 Run")
	}
	commit, err := s.Hub.LastChange(ctx, s.reportPath(run.ProjectID, id))
	if err != nil {
		return model.Report{}, err
	}
	report.HubCommit = commit
	return canonicalReport(report), nil
}
