package service

import (
	"fmt"
	"os"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func currentTrainAttemptAuthority(train model.TrainV2) (model.TrainV2Item, model.TrainV2Attempt, bool, error) {
	var activeItem model.TrainV2Item
	var activeAttempt model.TrainV2Attempt
	found := false
	for _, item := range train.Items {
		if item.ActiveAttemptNumber == 0 {
			continue
		}
		if item.ActiveAttemptNumber > uint64(len(item.Attempts)) {
			return model.TrainV2Item{}, model.TrainV2Attempt{}, false, fmt.Errorf("Train active Attempt is out of range")
		}
		if found {
			return model.TrainV2Item{}, model.TrainV2Attempt{}, false, fmt.Errorf("Train has ambiguous active Attempt authority")
		}
		activeItem = item
		activeAttempt = item.Attempts[item.ActiveAttemptNumber-1]
		if activeAttempt.Number != item.ActiveAttemptNumber {
			return model.TrainV2Item{}, model.TrainV2Attempt{}, false, fmt.Errorf("Train active Attempt identity is invalid")
		}
		found = true
	}
	return activeItem, activeAttempt, found, nil
}

func (s *Service) sharedRuntimeForAttempt(projectID string, train model.TrainV2, item model.TrainV2Item, attempt model.TrainV2Attempt) (trainv2.RuntimeBinding, error) {
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, projectID, train.ID)
	if err == nil {
		if runtime.TrainID != train.ID || runtime.ProjectID != projectID || runtime.ItemPosition != item.Position || runtime.TaskID != item.TaskID || runtime.AttemptNumber != attempt.Number || runtime.AgentID != attempt.AgentID || runtime.SessionKey != attempt.AirelaySessionKey {
			return trainv2.RuntimeBinding{}, fmt.Errorf("Train runtime does not match Attempt authority")
		}
		return runtime, nil
	}
	if !os.IsNotExist(err) {
		return trainv2.RuntimeBinding{}, err
	}
	projectConfig, ok := s.Config.Projects[projectID]
	if !ok || model.ValidateProjectCode(projectConfig.ProjectCode) != nil {
		return trainv2.RuntimeBinding{}, fmt.Errorf("project %q has no valid local project code for runtime recovery", projectID)
	}
	runtime, err = trainv2.RuntimeBindingFromAttempt(s.Config.StateDir, projectID, projectConfig.ProjectCode, train, item, attempt)
	if err != nil {
		return trainv2.RuntimeBinding{}, err
	}
	if err := fsutil.WriteJSONAtomic(trainv2.RuntimePath(s.Config.StateDir, projectID, train.ID), runtime, 0o600); err != nil {
		return trainv2.RuntimeBinding{}, fmt.Errorf("persist recovered Train runtime: %w", err)
	}
	return runtime, nil
}

// deriveExistingTrainAttemptAuthority makes the persisted Attempt the source
// of Agent/session ownership. Shared execution also reconstructs only the
// disposable runtime projection when it is missing; it never creates a new
// Attempt or resolves a new Agent for an active Attempt.
func (s *Service) deriveExistingTrainAttemptAuthority(projectID, trainID string, train model.TrainV2, requestedReasoning string) (ResolvedAgent, bool, error) {
	if s.Durability == nil {
		return ResolvedAgent{}, false, fmt.Errorf("Shared Train authority is unavailable")
	}
	{
		item, attempt, found, err := currentTrainAttemptAuthority(train)
		if err != nil || !found {
			return ResolvedAgent{}, false, err
		}
		if _, err := s.sharedRuntimeForAttempt(projectID, train, item, attempt); err != nil {
			return ResolvedAgent{}, false, err
		}
		reasoning := requestedReasoning
		if reasoning == "" {
			reasoning = model.ReasoningBestAvailable
		}
		if !validRoutingReasoning(reasoning) {
			return ResolvedAgent{}, false, fmt.Errorf("invalid recommended reasoning")
		}
		return ResolvedAgent{ProjectID: projectID, AgentID: attempt.AgentID, Role: model.AgentRoleCoding, RequestedReasoning: reasoning, ResolvedReasoning: reasoning, SessionKey: attempt.AirelaySessionKey}, true, nil
	}
}
