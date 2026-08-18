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
