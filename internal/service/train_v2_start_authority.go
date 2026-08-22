package service

import (
	"fmt"
	"os"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

// deriveExistingTrainAttemptAuthority makes the persisted Attempt the source
// of Agent/session ownership when a local Train runtime already exists. The
// runtime is only a consistency check; it is never an authority substitute.
func (s *Service) deriveExistingTrainAttemptAuthority(projectID, trainID string, train model.TrainV2, requestedReasoning string) (ResolvedAgent, bool, error) {
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, projectID, trainID)
	if err != nil {
		if os.IsNotExist(err) {
			return ResolvedAgent{}, false, nil
		}
		return ResolvedAgent{}, false, err
	}
	if runtime.TrainID != trainID || runtime.ProjectID != projectID || runtime.ItemPosition < 0 || runtime.ItemPosition >= len(train.Items) {
		return ResolvedAgent{}, false, fmt.Errorf("Train runtime identity is invalid")
	}
	item := train.Items[runtime.ItemPosition]
	if runtime.TaskID != item.TaskID || runtime.AttemptNumber == 0 || runtime.AttemptNumber > uint64(len(item.Attempts)) {
		return ResolvedAgent{}, false, fmt.Errorf("Train runtime does not identify an exact Attempt")
	}
	attempt := item.Attempts[runtime.AttemptNumber-1]
	if runtime.AgentID != attempt.AgentID || runtime.SessionKey != attempt.AirelaySessionKey {
		return ResolvedAgent{}, false, fmt.Errorf("Train runtime does not match Attempt authority")
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
