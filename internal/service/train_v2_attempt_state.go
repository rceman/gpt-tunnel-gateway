package service

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// checkTrainV2AttemptGraph is intentionally independent of the legacy
// Task->Run graph. A Train-v2 project is valid only when every non-queued item
// has canonical item-local Attempts; missing Attempts are invalid state.
func (s *Service) checkTrainV2AttemptGraph(ctx context.Context, snapshot *hub.ReadSnapshot, projectID string, result *StateCheckResult) {
	paths, err := snapshot.List(ctx, s.trainV2Root(projectID), ".json")
	if err != nil {
		result.Issues = append(result.Issues, stateIssue("TRAIN_V2_ATTEMPT_GRAPH_UNAVAILABLE", projectID, "", s.trainV2Root(projectID), err.Error()))
		result.OperationalTaskRunGraph = false
		return
	}
	for _, path := range paths {
		var train model.TrainV2
		if err := snapshot.ReadJSON(ctx, path, &train); err != nil {
			result.Issues = append(result.Issues, stateIssue("TRAIN_V2_ATTEMPT_GRAPH_INVALID", projectID, "", path, err.Error()))
			result.OperationalTaskRunGraph = false
			continue
		}
		if err := model.ValidateTrainV2(train); err != nil {
			result.Issues = append(result.Issues, stateIssue("TRAIN_V2_ATTEMPT_GRAPH_INVALID", projectID, "", path, err.Error()))
			result.OperationalTaskRunGraph = false
			continue
		}
		for _, item := range train.Items {
			if item.Status != model.TrainV2ItemQueued && len(item.Attempts) == 0 {
				result.Issues = append(result.Issues, stateIssue("TRAIN_V2_ATTEMPT_MISSING", projectID, item.TaskID, path, fmt.Sprintf("Train item %s has no item-local attempt", item.TaskID)))
				result.OperationalTaskRunGraph = false
			}
		}
	}
}
