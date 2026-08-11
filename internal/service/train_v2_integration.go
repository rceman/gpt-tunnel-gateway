package service

import (
	"context"
	"fmt"
	"time"

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
			return receipt, OperationResult{ProjectID: in.ProjectID, Status: receipt.Status}, nil
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
	if err := s.Git.Refresh(ctx, project); err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	laneHead, exists, err := s.Git.MirrorBranchHead(ctx, project, start.LaneBranch)
	if err != nil || !exists {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("Train lane branch is unavailable")
	}
	targetHead, exists, err := s.Git.MirrorBranchHead(ctx, project, policy.IntegrationBranch)
	if err != nil || !exists {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("integration branch is unavailable")
	}
	ancestor, err := s.Git.MirrorAncestor(ctx, project, targetHead, laneHead)
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	plan, err := trainv2.PlanIntegration(train, targetHead, ancestor)
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, err
	}
	if plan.Reconciliation {
		receipt := trainv2.IntegrationReceipt{SchemaVersion: 1, ProjectID: in.ProjectID, TrainID: in.TrainID, BaseRevision: start.BaseRevision, LaneHead: laneHead, TargetBefore: targetHead, ProofCandidate: train.FullProof.CandidateHead, Status: plan.Status, NextAction: plan.NextAction, Conflict: "integration target is not an ancestor of the proved Train lane", UpdatedAt: time.Now().UTC()}
		if recordErr := s.writeTrainV2IntegrationReceipt(ctx, receipt); recordErr != nil {
			return receipt, OperationResult{}, recordErr
		}
		return receipt, OperationResult{ProjectID: in.ProjectID, Status: plan.Status}, fmt.Errorf("Train integration requires bounded reconciliation")
	}
	if s.taskActivator == nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("Train activation is not configured")
	}
	pre, err := s.taskActivator(ctx, project, laneHead)
	if err != nil {
		return trainv2.IntegrationReceipt{}, OperationResult{}, fmt.Errorf("pre-integration activation failed: %w", err)
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
	now := time.Now().UTC()
	receipt := trainv2.IntegrationReceipt{SchemaVersion: 1, ProjectID: in.ProjectID, TrainID: in.TrainID, BaseRevision: start.BaseRevision, LaneHead: laneHead, TargetBefore: targetHead, IntegrationHead: targetHead, RuntimeHead: post.SourceHead, ProofCandidate: train.FullProof.CandidateHead, PreActivation: pre.Activation, PreSmoke: pre.Smoke, PostActivation: post.Activation, PostSmoke: post.Smoke, Status: "completed", NextAction: "complete", UpdatedAt: now}
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
	return receipt, OperationResult{Hub: tx, ProjectID: in.ProjectID, Status: receipt.Status}, nil
}
