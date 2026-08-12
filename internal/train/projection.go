package train

import (
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type ProjectProjection struct {
	TaskCounts      map[string]int
	TrainCounts     map[string]int
	CurrentTrain    string
	CurrentTask     string
	CurrentRun      string
	ActiveTrains    []string
	AmbiguousActive bool
	NextAction      string
}

// ProjectStatus derives the bounded Train v2 roadmap projection from portable
// Task and Train records only. It does not inspect Plan state or host bindings.
func ProjectStatus(tasks []model.TaskAuthoring, trains []model.TrainV2) ProjectProjection {
	projection := ProjectProjection{TaskCounts: map[string]int{}, TrainCounts: map[string]int{}, NextAction: "no pending Train v2 action"}
	for _, task := range tasks {
		projection.TaskCounts[task.Status]++
	}
	ordered := append([]model.TrainV2(nil), trains...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].UpdatedAt.Equal(ordered[j].UpdatedAt) {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].UpdatedAt.After(ordered[j].UpdatedAt)
	})
	hasPlannedTrain := false
	for _, train := range ordered {
		projection.TrainCounts[train.Status]++
		if train.Status == model.TrainV2Running || train.Status == model.TrainV2Paused || train.Status == model.TrainV2Blocked || train.Status == model.TrainV2ReadyForIntegration {
			projection.ActiveTrains = append(projection.ActiveTrains, train.ID)
		}
		for _, item := range train.Items {
			if projection.CurrentTrain == "" && (item.Status == model.TrainV2ItemRunning || item.Status == model.TrainV2ItemBlocked) {
				projection.CurrentTrain, projection.CurrentTask, projection.CurrentRun = train.ID, item.TaskID, item.RunID
				projection.NextAction = "observe Train " + train.ID + " item " + item.TaskID
			}
		}
		if projection.CurrentTrain == "" && train.Status == model.TrainV2Planned {
			hasPlannedTrain = true
			projection.NextAction = "start Train " + train.ID
		}
	}
	if len(projection.ActiveTrains) > 1 {
		projection.AmbiguousActive = true
		projection.CurrentTrain, projection.CurrentTask, projection.CurrentRun = "", "", ""
		projection.NextAction = "select one of active Trains: " + strings.Join(projection.ActiveTrains, ", ")
	}
	if projection.CurrentTrain == "" && !hasPlannedTrain {
		for _, task := range tasks {
			if task.Status == model.TaskAuthoringReady {
				projection.NextAction = "admit ready Task " + task.ID + " to a Train"
				break
			}
		}
	}
	return projection
}
