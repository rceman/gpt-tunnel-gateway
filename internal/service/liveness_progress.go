package service

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const progressTailLines = 4

func appendComponentError(errors *[]string, name string, err error) {
	if err != nil {
		*errors = append(*errors, name+": "+err.Error())
	}
}

func projectProgressFromInputs(tasks []TaskRecord, tasksErr error, status airelay.SessionStatus, statusErr error, tail airelay.Result, tailErr error) ProjectProgress {
	progress := ProjectProgress{
		AgentState:            status.State,
		ControllerReachable:   status.ControllerReachable,
		AirelayVersion:        status.AirelayVersion,
		ProtocolVersion:       status.ProtocolVersion,
		CapacityWarnings:      append([]string{}, status.CapacityWarnings...),
		ExitCode:              status.ExitCode,
		Tail:                  tail.Stdout,
		BlockerClassification: "none",
		RecommendedNextAction: "inspect Train-v2 item attempt",
		ComponentErrors:       []string{},
	}
	if statusErr != nil {
		appendComponentError(&progress.ComponentErrors, "agent_status", statusErr)
	}
	if tailErr != nil {
		appendComponentError(&progress.ComponentErrors, "agent_tail", tailErr)
	}
	if tasksErr != nil {
		appendComponentError(&progress.ComponentErrors, "tasks", tasksErr)
	}
	_ = tasks
	if len(progress.ComponentErrors) > 0 && progress.BlockerClassification == "none" {
		progress.BlockerClassification = "PROGRESS_COMPONENT_ERROR"
	}
	if progress.AgentState == "" {
		progress.AgentState = model.AgentStateUnknown
	}
	return progress
}

// projectProgress reads only the current agent/session snapshot. It never
// traverses Task->Run or reads operational /runs state after the Train-v2
// cutover.
func (s *Service) projectProgress(ctx context.Context, projectID string) (ProjectProgress, error) {
	local, err := s.projectConfig(projectID)
	if err != nil {
		return ProjectProgress{}, err
	}
	session := local.AirelaySessionKey
	if resolved, resolveErr := s.resolveAgentSession(ctx, projectID); resolveErr == nil {
		session = resolved
	}
	status, statusErr := s.Airelay.Status(ctx, session)
	tail, tailErr := s.Airelay.Tail(ctx, session, progressTailLines)
	progress := ProjectProgress{
		AgentState:            status.State,
		ControllerReachable:   status.ControllerReachable,
		AirelayVersion:        status.AirelayVersion,
		ProtocolVersion:       status.ProtocolVersion,
		CapacityWarnings:      append([]string{}, status.CapacityWarnings...),
		ExitCode:              status.ExitCode,
		Tail:                  tail.Stdout,
		BlockerClassification: "none",
		RecommendedNextAction: "inspect Train-v2 item attempt",
		ComponentErrors:       []string{},
	}
	if progress.AgentState == "" {
		progress.AgentState = model.AgentStateUnknown
	}
	appendComponentError(&progress.ComponentErrors, "agent_status", statusErr)
	appendComponentError(&progress.ComponentErrors, "agent_tail", tailErr)
	if len(progress.ComponentErrors) > 0 {
		progress.BlockerClassification = "PROGRESS_COMPONENT_ERROR"
	}
	return progress, nil
}
