package service

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
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
		if resumedReceipt, result, resumeErr, handled := s.resumeTrainV2IntegrationReceipt(ctx, in, receipt); handled {
			return resumedReceipt, result, resumeErr
		}
	}
	integrationLock, err := s.acquireTrainV2IntegrationLock(ctx, in.ProjectID)
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	defer integrationLock.Release()
	train, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	project, err := s.projectConfig(in.ProjectID)
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	configuration, err := s.ProjectConfigurationRead(ctx, in.ProjectID)
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	policy, err := s.ProjectWorkflowPolicyRead(ctx, in.ProjectID)
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	targetBranch := configuration.Integration.TargetBranch
	if targetBranch == "" {
		targetBranch = policy.IntegrationBranch
	}
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, in.ProjectID, in.TrainID)
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("read Train runtime: %w", err)
	}
	lane := project
	lane.Root = runtime.WorktreePath
	laneHead, laneBranch, laneClean, err := s.Git.CurrentHead(ctx, lane)
	if err != nil || !laneClean || laneBranch == "" {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("Train lane worktree is unavailable or changed")
	}
	item, attempt, found, authorityErr := currentTrainAttemptAuthority(train)
	if authorityErr != nil || !found {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("Train Attempt authority is unavailable")
	}
	start := trainv2.DeriveStartRecord(train, item, attempt, policy, model.Project{ID: in.ProjectID, DefaultBranch: project.DefaultBranch}, attempt.StartedAt)
	start.LaneBranch = laneBranch
	targetHead, exists, err := s.trainV2IntegrationTargetHead(ctx, project, targetBranch)
	if err != nil || !exists {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("integration branch is unavailable")
	}
	targetBefore := targetHead
	reconciliationReceipt, reconciliationResult, reconciliationErr, handled := s.reconcileTrainV2Integration(ctx, in, train, start, lane, laneHead, targetHead)
	if handled {
		return reconciliationReceipt, reconciliationResult, reconciliationErr
	}
	ancestor := true
	if train.FullProof == nil || train.FullProof.CandidateHead != laneHead {
		gateNames, gateErr := s.ResolveProjectGates(ctx, in.ProjectID, "integration")
		if gateErr != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, gateErr
		}
		fullGates, gateErr := s.executeProjectGatesWithProjectCommands(ctx, in.ProjectID, lane.Root, gateNames, "train")
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
	operation, err := s.integrationOperation(ctx, in, laneHead, targetBranch, targetHead, ancestor, time.Now().UTC())
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	if operation.Phase == trainv2.IntegrationPhaseRecoveryRequired {
		return trainv2.IntegrationReceipt{}, OperationResult{
			ProjectID: in.ProjectID,
			Status:    operation.Phase,
		}, fmt.Errorf("Train integration operation recovery_required")
	}
	preHook := integrationHookResult{Evidence: operation.PreResult}
	if operation.PreResult != "" {
		parse := parseIntegrationHookEvidence
		if len(configuration.Integration.Pre.Command) != 0 {
			parse = parseConfiguredIntegrationHookEvidence
		}
		preHook, err = parse(operation.PreResult, laneHead)
		if err != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("invalid persisted pre-integration hook evidence: %w", err)
		}
	}
	if operation.Phase == trainv2.IntegrationPhasePrePending {
		preHook, err = runIntegrationHook(ctx, configuration.Integration.Pre, lane.Root, laneHead)
		if err != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("pre-integration hook failed: %w", err)
		}
		operation, err = s.advanceIntegrationOperation(ctx, operation, trainv2.IntegrationPhasePreComplete, preHook.Evidence)
		if err != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, err
		}
	}
	if operation.Phase == trainv2.IntegrationPhasePreComplete {
		operation, err = s.advanceIntegrationOperation(ctx, operation, trainv2.IntegrationPhaseIntegratePending, "")
		if err != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, err
		}
	}
	if operation.Phase == trainv2.IntegrationPhaseIntegratePending {
		if targetHead != laneHead {
			if targetHead != operation.TargetBefore {
				return trainv2.IntegrationReceipt{}, OperationResult{
					ProjectID: in.ProjectID,
					Status:    trainv2.IntegrationPhaseRecoveryRequired,
				}, fmt.Errorf("Train integration operation recovery_required: target advanced unexpectedly")
			}
			if err := s.Git.PushFastForward(ctx, project, targetBranch, targetHead, laneHead); err != nil {
				return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("Train fast-forward push failed: %w", err)
			}
			if err := s.Git.Refresh(ctx, project); err != nil {
				return trainv2.IntegrationReceipt{}, OperationResult{}, err
			}
			if targetHead, exists, err = s.Git.MirrorBranchHead(ctx, project, targetBranch); err != nil || !exists || targetHead != laneHead {
				return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("integration branch did not reach proved Train head")
			}
		}
		operation, err = s.advanceIntegrationOperation(ctx, operation, trainv2.IntegrationPhaseIntegrateComplete, "")
		if err != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, err
		}
	}
	if operation.Phase == trainv2.IntegrationPhaseIntegrateComplete {
		operation, err = s.advanceIntegrationOperation(ctx, operation, trainv2.IntegrationPhasePostPending, "")
		if err != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, err
		}
	}
	postHook := integrationHookResult{Evidence: operation.PostResult}
	if operation.PostResult != "" {
		parse := parseIntegrationHookEvidence
		if len(configuration.Integration.Post.Command) != 0 {
			parse = parseConfiguredIntegrationHookEvidence
		}
		postHook, err = parse(operation.PostResult, laneHead)
		if err != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("invalid persisted post-integration hook evidence: %w", err)
		}
	}
	if operation.Phase == trainv2.IntegrationPhasePostPending {
		postHook, err = runIntegrationHook(ctx, configuration.Integration.Post, lane.Root, laneHead)
		if err != nil {
			return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("post-integration hook failed: %w", err)
		}
	}
	runtimeHead := laneHead
	if len(configuration.Integration.Post.Command) != 0 {
		runtimeHead = postHook.SourceHead
	}
	now := time.Now().UTC()
	receipt := trainv2.IntegrationReceipt{SchemaVersion: 1, ProjectID: in.ProjectID, TrainID: in.TrainID, BaseRevision: start.BaseRevision, LaneHead: laneHead, TargetBefore: targetBefore, IntegrationHead: laneHead, RuntimeHead: runtimeHead, ProofCandidate: train.FullProof.CandidateHead, PreActivation: preHook.Evidence, PreSmoke: preHook.Evidence, PostActivation: postHook.Evidence, PostSmoke: postHook.Evidence, Status: "completed", NextAction: "complete", UpdatedAt: now}
	if err := trainv2.ValidateIntegrationReceipt(receipt); err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	completed, operationResult, err := s.completeTrainV2Integration(ctx, in, train.Revision, laneHead, TaskActivationResult{SourceHead: runtimeHead}, receipt, project, start.LaneBranch)
	if err != nil {
		return completed, operationResult, err
	}
	if _, err := s.advanceIntegrationOperation(ctx, operation, trainv2.IntegrationPhaseCompleted, postHook.Evidence); err != nil {
		return completed, operationResult, err
	}
	return completed, operationResult, nil

}

// trainV2IntegrationTargetHead refreshes the managed mirror while the
// project-scoped integration lock is held, then resolves the exact target
// branch. A waiter must validate the current remote target after the previous
// integration owner releases the lock; a cached mirror ref is insufficient.
func (s *Service) trainV2IntegrationTargetHead(ctx context.Context, project config.ProjectConfig, branch string) (string, bool, error) {
	if err := s.Git.Refresh(ctx, project); err != nil {
		return "", false, err
	}
	return s.Git.MirrorBranchHead(ctx, project, branch)
}

// acquireTrainV2IntegrationLock is deliberately project-scoped: Git main is
// shared by all Trains in a project. A second eligible Train waits for the
// first lifecycle to finish instead of failing admission or racing main.
func (s *Service) acquireTrainV2IntegrationLock(ctx context.Context, projectID string) (*lockfile.Lock, error) {
	lockName := "train-v2-integration-" + projectID
	for {
		lock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), lockName)
		if err == nil {
			return lock, nil
		}
		if !lockfile.IsBusy(err) {
			return nil, err
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
