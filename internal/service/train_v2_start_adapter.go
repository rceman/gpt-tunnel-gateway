package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

// TrainV2StartResult is the service-facing view of the train start
// transition. The durable Hub record remains the safe model record; Runtime
// is explicitly Gateway-local.
type TrainV2StartResult = trainv2.StartResult

// TrainV2Start resolves Agent/session identity and delegates orchestration to
// internal/train. The service is intentionally only a wiring and authority
// adapter; it does not own Train v2 execution mechanics.
func (s *Service) TrainV2Start(ctx context.Context, in TrainV2StartInput) (trainv2.StartResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return trainv2.StartResult{}, err
	}
	if s.Durability != nil {
		return s.trainV2StartFromSharedRuntime(ctx, in)
	}
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return trainv2.StartResult{}, err
	}
	if strings.TrimSpace(in.StartedBy) == "" || strings.ContainsAny(in.StartedBy, "\x00\r\n") {
		return trainv2.StartResult{}, fmt.Errorf("started_by is required")
	}
	train, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return trainv2.StartResult{}, err
	}
	for _, item := range train.Items {
		task, taskErr := s.TaskAuthoringRead(ctx, in.ProjectID, item.TaskID)
		if taskErr != nil {
			return trainv2.StartResult{}, taskErr
		}
		if err := s.validateTaskDependencies(ctx, in.ProjectID, task); err != nil {
			return trainv2.StartResult{}, err
		}
	}
	project, err := s.ProjectRead(ctx, in.ProjectID)
	if err != nil {
		return trainv2.StartResult{}, err
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, in.ProjectID)
	if err != nil {
		return trainv2.StartResult{}, err
	}
	policy, err := s.ProjectWorkflowPolicyRead(ctx, in.ProjectID)
	if err != nil {
		return trainv2.StartResult{}, err
	}
	projectConfig, ok := s.Config.Projects[in.ProjectID]
	if !ok {
		return trainv2.StartResult{}, fmt.Errorf("project %q has no local runtime configuration", in.ProjectID)
	}
	resolved, err := s.ResolveAgent(ctx, AgentResolveInput{
		ProjectID:            in.ProjectID,
		Role:                 model.AgentRoleCoding,
		AgentID:              in.AgentID,
		RecommendedReasoning: in.RecommendedReasoning,
		RequireUsable:        true,
	})
	if err != nil {
		return trainv2.StartResult{}, err
	}
	if err := s.checkSessionAvailableForTrainAttempt(ctx, resolved.SessionKey, train.ID); err != nil {
		return trainv2.StartResult{}, err
	}
	return trainv2.Start(ctx, trainv2.StartInput{
		ProjectID:           in.ProjectID,
		TrainID:             in.TrainID,
		StartedBy:           in.StartedBy,
		AgentID:             in.AgentID,
		RequestedReasoning:  resolved.RequestedReasoning,
		ResolvedReasoning:   resolved.ResolvedReasoning,
		ResolvedAgentID:     resolved.AgentID,
		SessionKey:          resolved.SessionKey,
		AgentFallback:       resolved.Fallback,
		AgentFallbackReason: resolved.FallbackReason,
		ExpectedHubRevision: in.ExpectedHubRevision,
	}, trainv2.StartDependencies{
		Hub:               s.Hub,
		Git:               s.Git,
		Airelay:           s.Airelay,
		ProjectConfig:     projectConfig,
		Project:           project,
		Policy:            policy,
		Train:             train,
		GatewayID:         s.Config.GatewayID,
		ProjectCode:       identifiers.ProjectCode,
		SessionOrigin:     AgentSessionID(ctx),
		StateDir:          s.Config.StateDir,
		MaterializePacket: s.materializeTrainV2Packet,
		ReadTask: func(readCtx context.Context, projectID, taskID string) (model.TaskAuthoring, error) {
			return s.TaskAuthoringRead(readCtx, projectID, taskID)
		},
		ReadTaskInWorktree: func(worktree, projectID, taskID string) (model.TaskAuthoring, error) {
			var task model.TaskAuthoring
			if err := readWorktreeJSON(worktree, s.taskAuthoringPath(projectID, taskID), &task); err != nil {
				return model.TaskAuthoring{}, err
			}
			return task, nil
		},
		ValidateTaskMembershipInWorktree: s.validateTrainV2TaskMembershipInWorktree,
		Now:                              s.durableNow,
	})
}

// trainV2StartFromSharedRuntime is the local recovery/admission path used
// after Shared bootstrap. It deliberately accepts only an already materialized
// local Attempt runtime: an incomplete local projection fails closed instead
// of falling back to Hub fetches or creating a second execution state machine.
func (s *Service) trainV2StartFromSharedRuntime(ctx context.Context, in TrainV2StartInput) (trainv2.StartResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return trainv2.StartResult{}, err
	}
	if strings.TrimSpace(in.StartedBy) == "" || strings.ContainsAny(in.StartedBy, "\x00\r\n") {
		return trainv2.StartResult{}, fmt.Errorf("started_by is required")
	}
	train, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return trainv2.StartResult{}, err
	}
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, in.ProjectID, in.TrainID)
	if err != nil {
		return trainv2.StartResult{}, fmt.Errorf("Shared Train has no local Attempt runtime; refusing Hub fallback: %w", err)
	}
	if runtime.TrainID != train.ID || runtime.ItemPosition < 0 || runtime.ItemPosition >= len(train.Items) {
		return trainv2.StartResult{}, fmt.Errorf("local Train Attempt runtime ownership mismatch")
	}
	item := train.Items[runtime.ItemPosition]
	if item.TaskID != runtime.TaskID || runtime.AttemptNumber == 0 || runtime.AttemptNumber > uint64(len(item.Attempts)) {
		return trainv2.StartResult{}, fmt.Errorf("local Train Attempt item binding is invalid")
	}
	attempt := item.Attempts[runtime.AttemptNumber-1]
	if attempt.Status != model.TrainV2AttemptRunning && attempt.Status != model.TrainV2AttemptRecovered {
		return trainv2.StartResult{}, fmt.Errorf("local Train Attempt is not resumable")
	}
	if in.AgentID != "" && in.AgentID != attempt.AgentID {
		return trainv2.StartResult{}, fmt.Errorf("Train Attempt Agent binding mismatch")
	}
	if err := trainv2.ValidateRuntimeBinding(runtime, s.Config.StateDir); err != nil {
		return trainv2.StartResult{}, err
	}
	return trainv2.StartResult{
		Record: model.TrainV2StartRecord{
			SchemaVersion: model.TrainV2StartSchemaVersion, ProjectID: train.ProjectID, TrainID: train.ID,
			Status: model.TrainV2StartActive, CurrentItemPosition: item.Position,
			CurrentAttemptNumber: attempt.Number, CurrentTaskID: item.TaskID,
			CurrentTaskRevision: item.TaskRevision, CurrentTaskRevisionSHA256: item.TaskRevisionSHA256,
			BaseRevision: attempt.StartHead, LaneBranch: "train/" + train.ID,
			StartedAt: attempt.StartedAt,
		},
		ItemPosition: item.Position, Attempt: attempt, Runtime: runtime,
	}, nil
}
