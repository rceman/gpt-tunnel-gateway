package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/gates"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

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
		if _, retireErr := trainv2.RetireRuntimeForRestart(s.Config.StateDir, in.ProjectID, in.TrainID, start.CurrentAttemptNumber); retireErr != nil {
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
	return s.completeTrainV2Integration(ctx, in, train.Revision, laneHead, post, receipt, project, start.LaneBranch)

}
