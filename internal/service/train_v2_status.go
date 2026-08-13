package service

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func (s *Service) projectStatusTrainV2(ctx context.Context, id string, local config.ProjectConfig) (ProjectStatus, error) {
	componentCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	project, projectErr := s.ProjectRead(componentCtx, id)
	worktree, worktreeErr := s.Git.WorktreeStatus(componentCtx, local)
	policy, policyErr := s.ProjectWorkflowPolicyRead(componentCtx, id)
	configurationStatus := s.projectConfigurationStatus(componentCtx, id)
	tasks, taskErr := s.taskAuthoringAll(componentCtx, id)
	trains, trainErr := s.readTrainV2Records(componentCtx, id)
	hubRevision, hubErr := s.hubRevision(componentCtx)
	agentSession := local.AirelaySessionKey
	if resolved, resolveErr := s.resolveAgentSession(componentCtx, id); resolveErr == nil {
		agentSession = resolved
	}
	agentStatus, agentStatusErr := s.Airelay.Status(componentCtx, agentSession)
	agentTail, agentTailErr := s.Airelay.Tail(componentCtx, agentSession, progressTailLines)

	projection := TrainV2ProjectStatus{ExecutionModel: "train_v2", TaskCounts: map[string]int{}, TrainCounts: map[string]int{}, NextAction: "no pending Train v2 action"}
	if taskErr == nil && trainErr == nil {
		pure := trainv2.ProjectStatus(tasks, trains)
		projection.TaskCounts, projection.TrainCounts = pure.TaskCounts, pure.TrainCounts
		projection.CurrentTrain, projection.CurrentTask, projection.CurrentAttempt, projection.NextAction = pure.CurrentTrain, pure.CurrentTask, pure.CurrentAttempt, pure.NextAction
		projection.ActiveTrains, projection.AmbiguousActive = pure.ActiveTrains, pure.AmbiguousActive
	}
	progress := ProjectProgress{AgentState: agentStatus.State, ControllerReachable: agentStatus.ControllerReachable, AirelayVersion: agentStatus.AirelayVersion, ProtocolVersion: agentStatus.ProtocolVersion, CapacityWarnings: append([]string{}, agentStatus.CapacityWarnings...), ExitCode: agentStatus.ExitCode, Tail: agentTail.Stdout, BlockerClassification: "none", RecommendedNextAction: projection.NextAction, ComponentErrors: []string{}}
	if progress.AgentState == "" {
		progress.AgentState = model.AgentStateUnknown
	}
	appendComponentError(&progress.ComponentErrors, "project", projectErr)
	appendComponentError(&progress.ComponentErrors, "worktree", worktreeErr)
	appendComponentError(&progress.ComponentErrors, "workflow_policy", policyErr)
	appendComponentError(&progress.ComponentErrors, "tasks", taskErr)
	appendComponentError(&progress.ComponentErrors, "trains", trainErr)
	appendComponentError(&progress.ComponentErrors, "hub_revision", hubErr)
	if agentStatusErr != nil && !agentStatus.ControllerReachable {
		appendComponentError(&progress.ComponentErrors, "agent_status", agentStatusErr)
	}
	appendComponentError(&progress.ComponentErrors, "agent_tail", agentTailErr)
	internalPaths := []string{s.Config.StateDir, local.Root, local.Mirror, local.AirelaySessionKey}
	for _, internal := range internalPaths {
		if internal != "" {
			progress.Tail = strings.ReplaceAll(progress.Tail, internal, "[gateway-internal-value]")
		}
	}
	sort.Strings(progress.ComponentErrors)
	if projectErr != nil {
		return ProjectStatus{}, projectErr
	}
	if policyErr != nil {
		return ProjectStatus{}, policyErr
	}
	return ProjectStatus{Project: project, Local: local, Worktree: worktree, Plan: retiredPlanStatus(id), HubRevision: hubRevision, Progress: progress, WorkflowPolicy: workflowPolicyStatus(policy, policyErr, model.Plan{}, nil), ProjectConfiguration: configurationStatus, TrainV2: &projection}, nil
}

// retiredPlanStatus keeps the legacy Plan field schema-valid after Train v2
// cutover without presenting Plan as current operational authority.
func retiredPlanStatus(projectID string) model.PlanStatus {
	return model.PlanStatus{
		SchemaVersion: model.PlanSchemaVersion,
		ProjectID:     projectID,
		Queue:         []string{},
		Sections:      []string{},
	}
}
