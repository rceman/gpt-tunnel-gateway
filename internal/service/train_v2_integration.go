package service

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gates"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

type TrainV2IntegrateResult = trainv2.IntegrationReceipt

func trainV2IntegrationPath(projectID, trainID string) string {
	return "gpt-tunnel/v1/projects/" + projectID + "/trains-v2/" + trainID + ".integration.json"
}

func (s *Service) readTrainV2IntegrationReceipt(ctx context.Context, projectID, trainID string) (trainv2.IntegrationReceipt, error) {
	var receipt trainv2.IntegrationReceipt
	if err := s.Hub.ReadJSON(ctx, trainV2IntegrationPath(projectID, trainID), &receipt); err != nil {
		return trainv2.IntegrationReceipt{}, err
	}
	if err := trainv2.ValidateIntegrationReceipt(receipt); err != nil {
		return trainv2.IntegrationReceipt{}, err
	}
	return receipt, nil
}

func (s *Service) writeTrainV2IntegrationReceipt(ctx context.Context, receipt trainv2.IntegrationReceipt) error {
	if err := trainv2.ValidateIntegrationReceipt(receipt); err != nil {
		return err
	}
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return err
	}
	_, err = s.Hub.Transact(ctx, expected, "gateway: record train v2 reconciliation "+receipt.TrainID, func(worktree string) ([]string, error) {
		path := trainV2IntegrationPath(receipt.ProjectID, receipt.TrainID)
		if err := hub.WriteJSON(worktree, path, receipt); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	return err
}

func (s *Service) TrainV2Integrate(ctx context.Context, in TrainV2IntegrateInput) (trainv2.IntegrationReceipt, OperationResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	if receipt, err := s.readTrainV2IntegrationReceipt(ctx, in.ProjectID, in.TrainID); err == nil {
		if receipt.Status == "completed" {
			project, projectErr := s.projectConfig(in.ProjectID)
			if projectErr != nil {
				return trainv2.IntegrationReceipt{}, OperationResult{}, projectErr
			}
			startPath := hub.ProtocolRoot + "/projects/" + in.ProjectID + "/train-v2-starts/" + in.TrainID + ".json"
			var start model.TrainV2StartRecord
			if startErr := s.Hub.ReadJSON(ctx, startPath, &start); startErr != nil {
				return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("read completed Train start: %w", startErr)
			}
			if cleanupErr := s.releaseTrainRuntime(ctx, project, in.ProjectID, in.TrainID, start.LaneBranch, receipt.LaneHead); cleanupErr != nil {
				return trainv2.IntegrationReceipt{}, OperationResult{}, cleanupErr
			}
			return receipt, OperationResult{ProjectID: in.ProjectID, Status: receipt.Status}, nil
		}
		if receipt.Status == "reconciliation_blocked" {
			return receipt, OperationResult{ProjectID: in.ProjectID, Status: receipt.Status}, fmt.Errorf("Train reconciliation is blocked; bounded Agent correction is required")
		}
		if receipt.Status == "reconciliation_complete" || receipt.Status == "reconciliation_requires_restart" {
			return s.finishTrainReconciliationRestart(ctx, in.ProjectID, in.TrainID, receipt)
		}
	}
	train, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	var start model.TrainV2StartRecord
	startPath := hub.ProtocolRoot + "/projects/" + in.ProjectID + "/train-v2-starts/" + in.TrainID + ".json"
	if err := s.Hub.ReadJSON(ctx, startPath, &start); err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("read Train start: %w", err)
	}
	project, err := s.projectConfig(in.ProjectID)
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	policy, err := s.ProjectWorkflowPolicyRead(ctx, in.ProjectID)
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, in.ProjectID, in.TrainID)
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("read Train runtime: %w", err)
	}
	lane := project
	lane.Root = runtime.WorktreePath
	laneHead, laneBranch, laneClean, err := s.Git.CurrentHead(ctx, lane)
	if err != nil || !laneClean || laneBranch != start.LaneBranch {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("Train lane worktree is unavailable or changed")
	}
	targetHead, exists, err := s.Git.MirrorBranchHead(ctx, project, policy.IntegrationBranch)
	if err != nil || !exists {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("integration branch is unavailable")
	}
	targetBefore := targetHead
	ancestor, err := s.Git.IsAncestor(ctx, lane.Root, targetHead, laneHead)
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	if !ancestor {
		commits, logErr := s.Git.LocalLog(ctx, lane.Root, start.BaseRevision, laneHead, s.Config.MaxListItems)
		if logErr != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("read owned Train reconciliation range: %w", logErr)
		}
		commitIDs := make([]string, 0, len(commits))
		for _, commit := range commits {
			commitIDs = append(commitIDs, commit.SHA)
		}
		reconciledHead, _, replayErr := s.Git.ReplayTrainCommits(ctx, lane, targetHead, commitIDs)
		if replayErr != nil {
			receipt := trainv2.IntegrationReceipt{SchemaVersion: 1, ProjectID: in.ProjectID, TrainID: in.TrainID, BaseRevision: start.BaseRevision, LaneHead: laneHead, TargetBefore: targetHead, Status: "reconciliation_blocked", NextAction: "bounded_agent_correction", Conflict: replayErr.Error(), UpdatedAt: time.Now().UTC()}
			if recordErr := s.writeTrainV2IntegrationReceipt(ctx, receipt); recordErr != nil {
				return receipt, OperationResult{}, recordErr
			}
			return receipt, OperationResult{ProjectID: in.ProjectID, Status: receipt.Status}, replayErr
		}
		updatedTrain, rebindErr := trainv2.ResetImplementationProofsForRestart(train, time.Now().UTC())
		if rebindErr != nil {
			_ = s.Git.ResetTrainWorktree(context.Background(), lane, laneHead)
			return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("reset Train proof for restart: %w", rebindErr)
		}
		receipt := trainv2.IntegrationReceipt{SchemaVersion: 1, ProjectID: in.ProjectID, TrainID: in.TrainID, BaseRevision: start.BaseRevision, LaneHead: targetHead, TargetBefore: targetHead, Status: "reconciliation_requires_restart", NextAction: "restart_train_items_from_refreshed_target", Conflict: fmt.Sprintf("discarded replay head %s; prior item proofs and reviews were invalidated", reconciledHead), UpdatedAt: time.Now().UTC()}
		if recordErr := s.persistTrainV2Reconciliation(ctx, in.ProjectID, in.TrainID, train.Revision, updatedTrain, receipt); recordErr != nil {
			if restoreErr := s.Git.ResetTrainWorktree(context.Background(), lane, laneHead); restoreErr != nil {
				return receipt, OperationResult{}, fmt.Errorf("record reconciliation reset: %w; restore original Train lane: %v", recordErr, restoreErr)
			}
			return receipt, OperationResult{}, recordErr
		}
		if resetErr := s.Git.ResetTrainWorktree(ctx, lane, targetHead); resetErr != nil {
			return receipt, OperationResult{ProjectID: in.ProjectID, Status: receipt.Status}, fmt.Errorf("Train reconciliation is recorded but local replay reset is pending: %w", resetErr)
		}
		if _, retireErr := trainv2.RetireRuntimeForRestart(s.Config.StateDir, in.ProjectID, in.TrainID, start.RunID); retireErr != nil {
			return receipt, OperationResult{ProjectID: in.ProjectID, Status: receipt.Status}, fmt.Errorf("Train reconciliation is recorded but local execution retirement is pending: %w", retireErr)
		}
		return receipt, OperationResult{ProjectID: in.ProjectID, Status: receipt.Status}, fmt.Errorf("Train reconciliation requires restart from the refreshed target; replay was discarded and item proofs require re-execution")
	}
	if train.FullProof == nil || train.FullProof.CandidateHead != laneHead {
		gateNames, gateErr := s.ResolveProjectGates(ctx, in.ProjectID, "integration")
		if gateErr != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, gateErr
		}
		fullGates, gateErr := s.executeProjectGatesWithTestReuse(ctx, in.ProjectID, lane.Root, gateNames, gates.FullTestScope())
		if gateErr != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, gateErr
		}
		if gateErr = validateProjectGateEvidence(fullGates, gateNames); gateErr != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, gateErr
		}
		updatedTrain, proofErr := trainv2.RecordFullProof(train, laneHead, fullGates, time.Now().UTC())
		if proofErr != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, proofErr
		}
		if err := s.persistTrainV2(ctx, in.ProjectID, in.TrainID, train.Revision, updatedTrain); err != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, err
		}
		train = updatedTrain
	}
	plan, err := trainv2.PlanIntegration(train, targetHead, ancestor)
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	if plan.Reconciliation {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("Train integration target still requires reconciliation")
	}
	if s.taskActivator == nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("Train activation is not configured")
	}
	pre, err := s.taskActivator(ctx, lane, laneHead)
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("pre-integration activation failed: %w", err)
	}
	if pre.SourceHead != laneHead {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("pre-integration activation did not prove the Train source head")
	}
	if plan.Status != "already_integrated" {
		if err := s.Git.PushFastForward(ctx, project, policy.IntegrationBranch, targetHead, laneHead); err != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("Train fast-forward push failed: %w", err)
		}
		if err := s.Git.Refresh(ctx, project); err != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, err
		}
		if targetHead, exists, err = s.Git.MirrorBranchHead(ctx, project, policy.IntegrationBranch); err != nil || !exists || targetHead != laneHead {
			return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("integration branch did not reach proved Train head")
		}
	}
	post, err := s.taskActivator(ctx, project, laneHead)
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("post-integration activation failed: %w", err)
	}
	if post.SourceHead != laneHead {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("post-integration activation did not prove the merged source head")
	}
	now := time.Now().UTC()
	receipt := trainv2.IntegrationReceipt{SchemaVersion: 1, ProjectID: in.ProjectID, TrainID: in.TrainID, BaseRevision: start.BaseRevision, LaneHead: laneHead, TargetBefore: targetBefore, IntegrationHead: laneHead, RuntimeHead: post.SourceHead, ProofCandidate: train.FullProof.CandidateHead, PreActivation: pre.Activation, PreSmoke: pre.Smoke, PostActivation: post.Activation, PostSmoke: post.Smoke, Status: "completed", NextAction: "complete", UpdatedAt: now}
	if err := trainv2.ValidateIntegrationReceipt(receipt); err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, err
		}
	}
	var integrated model.TrainV2
	tx, err := s.Hub.Transact(ctx, expected, "gateway: integrate Train v2 "+in.TrainID, func(worktree string) ([]string, error) {
		var latest model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), &latest); err != nil {
			return nil, err
		}
		if latest.Revision != train.Revision || latest.FullProof == nil || latest.FullProof.CandidateHead != laneHead {
			return nil, fmt.Errorf("Train changed before integration")
		}
		integrated, err = trainv2.MarkIntegrated(latest, laneHead, post.SourceHead, now)
		if err != nil {
			return nil, err
		}
		trainPath := s.trainV2Path(in.ProjectID, in.TrainID)
		receiptPath := trainV2IntegrationPath(in.ProjectID, in.TrainID)
		if err := hub.WriteJSON(worktree, trainPath, integrated); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, receiptPath, receipt); err != nil {
			return nil, err
		}
		return []string{trainPath, receiptPath}, nil
	})
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	if err := s.releaseTrainRuntime(ctx, project, in.ProjectID, in.TrainID, start.LaneBranch, laneHead); err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	return receipt, OperationResult{Hub: tx, ProjectID: in.ProjectID, Status: receipt.Status}, nil
}

func (s *Service) releaseTrainRuntime(ctx context.Context, project config.ProjectConfig, projectID, trainID, branch, head string) error {
	if err := s.Git.RemoveTrainWorktree(ctx, project, s.Config.StateDir, projectID, trainID); err != nil {
		return fmt.Errorf("release Train worktree: %w", err)
	}
	if err := s.Git.DeleteTrainBranch(ctx, project, branch, head); err != nil {
		return fmt.Errorf("release Train lane branch: %w", err)
	}
	if err := os.Remove(trainv2.RuntimePath(s.Config.StateDir, projectID, trainID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("release Train runtime binding: %w", err)
	}
	return nil
}

func (s *Service) persistTrainV2(ctx context.Context, projectID, trainID string, revision int, updated model.TrainV2) error {
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return err
	}
	_, err = s.Hub.Transact(ctx, expected, "gateway: persist Train v2 proof "+trainID, func(worktree string) ([]string, error) {
		var current model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(projectID, trainID), &current); err != nil {
			return nil, err
		}
		if current.Revision != revision {
			return nil, fmt.Errorf("Train v2 changed before proof persistence")
		}
		if err := hub.WriteJSON(worktree, s.trainV2Path(projectID, trainID), updated); err != nil {
			return nil, err
		}
		return []string{s.trainV2Path(projectID, trainID)}, nil
	})
	return err
}

func (s *Service) persistTrainV2Reconciliation(ctx context.Context, projectID, trainID string, revision int, updated model.TrainV2, receipt trainv2.IntegrationReceipt) error {
	if err := model.ValidateTrainV2(updated); err != nil {
		return err
	}
	if err := trainv2.ValidateIntegrationReceipt(receipt); err != nil {
		return err
	}
	expected, err := s.hubRevision(ctx)
	if err != nil {
		return err
	}
	_, err = s.Hub.Transact(ctx, expected, "gateway: record Train reconciliation restart "+trainID, func(worktree string) ([]string, error) {
		var current model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(projectID, trainID), &current); err != nil {
			return nil, err
		}
		if current.Revision != revision {
			return nil, fmt.Errorf("Train v2 changed before reconciliation reset persistence")
		}
		if err := hub.WriteJSON(worktree, s.trainV2Path(projectID, trainID), updated); err != nil {
			return nil, err
		}
		receiptPath := trainV2IntegrationPath(projectID, trainID)
		if err := hub.WriteJSON(worktree, receiptPath, receipt); err != nil {
			return nil, err
		}
		return []string{s.trainV2Path(projectID, trainID), receiptPath}, nil
	})
	return err
}

func (s *Service) finishTrainReconciliationRestart(ctx context.Context, projectID, trainID string, receipt trainv2.IntegrationReceipt) (trainv2.IntegrationReceipt, OperationResult, error) {
	project, err := s.projectConfig(projectID)
	if err != nil {
		return receipt, OperationResult{ProjectID: projectID, Status: receipt.Status}, err
	}
	startPath := hub.ProtocolRoot + "/projects/" + projectID + "/train-v2-starts/" + trainID + ".json"
	var start model.TrainV2StartRecord
	if err := s.Hub.ReadJSON(ctx, startPath, &start); err != nil {
		return receipt, OperationResult{ProjectID: projectID, Status: receipt.Status}, fmt.Errorf("read reconciled Train start: %w", err)
	}
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, projectID, trainID)
	if err != nil {
		return receipt, OperationResult{ProjectID: projectID, Status: receipt.Status}, fmt.Errorf("reconciled Train runtime is unavailable: %w", err)
	}
	lane := project
	lane.Root = runtime.WorktreePath
	target := receipt.LaneHead
	if receipt.Status == "reconciliation_complete" && receipt.TargetBefore != "" {
		target = receipt.TargetBefore
	}
	currentHead, currentBranch, clean, err := s.Git.CurrentHead(ctx, lane)
	if err != nil || !clean || currentBranch != start.LaneBranch {
		return receipt, OperationResult{ProjectID: projectID, Status: receipt.Status}, fmt.Errorf("reconciled Train lane is not active and clean")
	}
	if currentHead != target {
		if err := s.Git.ResetTrainWorktree(ctx, lane, target); err != nil {
			return receipt, OperationResult{ProjectID: projectID, Status: receipt.Status}, fmt.Errorf("finish local reconciliation reset: %w", err)
		}
	}
	if _, err := trainv2.RetireRuntimeForRestart(s.Config.StateDir, projectID, trainID, start.RunID); err != nil {
		return receipt, OperationResult{ProjectID: projectID, Status: receipt.Status}, fmt.Errorf("finish local execution retirement: %w", err)
	}
	return receipt, OperationResult{ProjectID: projectID, Status: receipt.Status}, fmt.Errorf("Train reconciliation requires restart from the refreshed target; it is not integrated")
}
